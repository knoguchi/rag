package service

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"strings"
	"sync"
	"time"

	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/auth"
	"github.com/knoguchi/rag/internal/ids"
	"github.com/knoguchi/rag/internal/ragcore"
	"github.com/knoguchi/rag/internal/repository"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// DocumentService implements ragv1.DocumentServiceServer
type DocumentService struct {
	ragv1.UnimplementedDocumentServiceServer

	docRepo    repository.DocumentRepository
	tenantRepo repository.TenantRepository
	engine     *ragcore.Engine
	httpClient *http.Client
	wg         sync.WaitGroup
}

// NewDocumentService creates a new DocumentService
func NewDocumentService(
	docRepo repository.DocumentRepository,
	tenantRepo repository.TenantRepository,
	engine *ragcore.Engine,
) *DocumentService {
	return &DocumentService{
		docRepo:    docRepo,
		tenantRepo: tenantRepo,
		engine:     engine,
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
}

// ingestTimeout bounds async document processing. Contextual ingestion runs
// one LLM call per chunk, so it gets a much larger budget.
func ingestTimeout(cfg repository.TenantConfig) time.Duration {
	if cfg.ContextualRetrievalEnabled {
		return 30 * time.Minute
	}
	return 10 * time.Minute
}

// authedTenant returns the authenticated tenant or an Unauthenticated error.
func authedTenant(ctx context.Context) (*auth.TenantInfo, error) {
	return auth.RequireTenant(ctx)
}

// tenantForProcessing adapts the authenticated tenant info to the repository
// shape used by the async ingestion pipeline.
func tenantForProcessing(t *auth.TenantInfo) *repository.Tenant {
	return &repository.Tenant{ID: t.ID, Name: t.Name, Config: t.Config}
}

// Shutdown waits for all in-flight document processing goroutines to complete.
func (s *DocumentService) Shutdown(ctx context.Context) error {
	done := make(chan struct{})
	go func() {
		s.wg.Wait()
		close(done)
	}()
	select {
	case <-done:
		slog.Info("all document processing goroutines completed")
		return nil
	case <-ctx.Done():
		slog.Warn("shutdown timeout waiting for document processing")
		return ctx.Err()
	}
}

// IngestDocument ingests raw text content
func (s *DocumentService) IngestDocument(ctx context.Context, req *ragv1.IngestDocumentRequest) (*ragv1.IngestDocumentResponse, error) {
	if req.Content == "" {
		return nil, status.Error(codes.InvalidArgument, "content is required")
	}
	const maxContentSize = 10 * 1024 * 1024 // 10MB
	if len(req.Content) > maxContentSize {
		return nil, status.Errorf(codes.InvalidArgument, "content too large: %d bytes (max %d)", len(req.Content), maxContentSize)
	}

	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}
	tenantID := tenant.ID

	// Calculate content hash for deduplication
	// Include source URL in hash so different pages with similar content are not deduplicated
	contentHash := hashContent(req.Source + "\n" + req.Content)

	slog.Debug("ingesting document",
		"tenant_id", tenantID,
		"source", req.Source,
		"content_len", len(req.Content),
		"hash_prefix", contentHash[:16],
	)

	// Check for duplicate document (same source + content = true duplicate)
	existingDoc, err := s.docRepo.GetByHash(ctx, tenantID, contentHash)
	if err == nil && existingDoc != nil {
		slog.Info("duplicate document found", "doc_id", existingDoc.ID)
		return &ragv1.IngestDocumentResponse{
			DocumentId: ids.Format(existingDoc.ID),
			Status:     convertStatus(existingDoc.Status),
		}, nil
	}

	// Same source but different content means the page changed: replace the
	// stale versions instead of accumulating them (re-crawls, auto-indexing
	// of pages the user revisits). Only applies to explicit sources.
	if req.Source != "" {
		stale, err := s.docRepo.ListBySource(ctx, tenantID, req.Source)
		if err != nil {
			slog.Warn("failed to check for stale documents", "error", err, "source", req.Source)
		}
		for _, old := range stale {
			slog.Info("replacing stale document for source",
				"doc_id", old.ID, "source", req.Source, "tenant_id", tenantID)
			if err := s.engine.DeleteDocument(ctx, tenantID.UUIDString(), old.ID.UUIDString()); err != nil {
				slog.Warn("failed to delete stale vectors", "error", err, "doc_id", old.ID)
			}
			if err := s.docRepo.DeleteChunks(ctx, tenantID, old.ID); err != nil {
				slog.Warn("failed to delete stale chunks", "error", err, "doc_id", old.ID)
			}
			if err := s.docRepo.Delete(ctx, tenantID, old.ID); err != nil {
				slog.Warn("failed to delete stale document", "error", err, "doc_id", old.ID)
			}
		}
	}

	// Create document record
	now := time.Now()
	docID := ids.New()
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
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout(tenant.Config))
		defer cancel()
		s.processDocument(ctx, doc, req.Content, tenantForProcessing(tenant))
	}()

	return &ragv1.IngestDocumentResponse{
		DocumentId: ids.Format(docID),
		Status:     ragv1.DocumentStatus_DOCUMENT_STATUS_PROCESSING,
	}, nil
}

// IngestURL fetches and ingests content from a URL
func (s *DocumentService) IngestURL(ctx context.Context, req *ragv1.IngestURLRequest) (*ragv1.IngestDocumentResponse, error) {
	if req.Url == "" {
		return nil, status.Error(codes.InvalidArgument, "url is required")
	}

	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}
	tenantID := tenant.ID

	// Create document record first with PENDING status. The content hash is
	// a unique per-document placeholder until the URL is fetched — an empty
	// hash would collide on UNIQUE(tenant_id, content_hash) with concurrent
	// or previously interrupted URL ingests.
	now := time.Now()
	docID := ids.New()
	doc := &repository.Document{
		ID:          docID,
		TenantID:    tenantID,
		Source:      req.Url,
		ContentHash: hashContent("pending:" + docID.String()),
		Status:      "PENDING",
		Metadata:    req.Metadata,
		CreatedAt:   now,
		UpdatedAt:   now,
	}

	if err := s.docRepo.Create(ctx, doc); err != nil {
		return nil, status.Errorf(codes.Internal, "failed to create document: %v", err)
	}

	// Fetch and process URL asynchronously
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		ctx, cancel := context.WithTimeout(context.Background(), ingestTimeout(tenant.Config))
		defer cancel()
		s.processURL(ctx, doc, req.Url, req.UseHeadless, tenantForProcessing(tenant))
	}()

	return &ragv1.IngestDocumentResponse{
		DocumentId: ids.Format(docID),
		Status:     ragv1.DocumentStatus_DOCUMENT_STATUS_PENDING,
	}, nil
}

// GetDocument retrieves a document by ID
func (s *DocumentService) GetDocument(ctx context.Context, req *ragv1.GetDocumentRequest) (*ragv1.Document, error) {
	if req.Id == "" {
		return nil, status.Error(codes.InvalidArgument, "id is required")
	}

	id, err := ids.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid document ID format")
	}

	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}

	doc, err := s.docRepo.GetByID(ctx, tenant.ID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get document: %v", err)
	}

	return s.documentToProto(doc), nil
}

// ListDocuments lists documents for the authenticated tenant
func (s *DocumentService) ListDocuments(ctx context.Context, req *ragv1.ListDocumentsRequest) (*ragv1.ListDocumentsResponse, error) {
	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}
	tenantID := tenant.ID

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

	id, err := ids.Parse(req.Id)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid document ID format")
	}

	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}

	// Verify the document belongs to the authenticated tenant
	doc, err := s.docRepo.GetByID(ctx, tenant.ID, id)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
		return nil, status.Errorf(codes.Internal, "failed to get document: %v", err)
	}

	// Delete vectors from vector store
	if err := s.engine.DeleteDocument(ctx, tenant.ID.UUIDString(), doc.ID.UUIDString()); err != nil {
		slog.Warn("failed to delete vectors", "error", err, "doc_id", doc.ID)
	}

	// Delete chunks from repository
	if err := s.docRepo.DeleteChunks(ctx, tenant.ID, id); err != nil {
		slog.Warn("failed to delete chunks", "error", err, "doc_id", doc.ID)
	}

	// Delete document
	if err := s.docRepo.Delete(ctx, tenant.ID, id); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, status.Error(codes.NotFound, "document not found")
		}
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

	docID, err := ids.Parse(req.DocumentId)
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

	tenant, err := authedTenant(ctx)
	if err != nil {
		return nil, err
	}

	chunks, err := s.docRepo.GetChunks(ctx, tenant.ID, docID, pageSize, offset)
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

// ingestedToDocumentChunks converts engine-ingested chunks to repository
// DocumentChunks, preserving the chunk IDs used as vector point IDs.
func ingestedToDocumentChunks(chunks []ragcore.IngestedChunk, tenantID, documentID ids.ID) []*repository.DocumentChunk {
	docChunks := make([]*repository.DocumentChunk, len(chunks))
	now := time.Now()

	for i, c := range chunks {
		id, err := ids.Parse(c.ID)
		if err != nil {
			id = ids.New()
		}
		docChunks[i] = &repository.DocumentChunk{
			ID:         id,
			DocumentID: documentID,
			TenantID:   tenantID,
			ChunkIndex: c.Index,
			Content:    c.Content,
			Metadata:   c.Metadata,
			CreatedAt:  now,
		}
	}

	return docChunks
}

// processDocument processes a document asynchronously
func (s *DocumentService) processDocument(ctx context.Context, doc *repository.Document, content string, tenant *repository.Tenant) {
	// Update status to PROCESSING
	doc.Status = "PROCESSING"
	doc.UpdatedAt = time.Now()
	if err := s.docRepo.Update(ctx, doc); err != nil {
		slog.Error("failed to update document status", "error", err, "doc_id", doc.ID)
	}

	// Run the core ingestion pipeline (chunk -> persist -> embed -> upsert)
	ingested, err := s.engine.Ingest(ctx, ragcore.IngestInput{
		Namespace:  doc.TenantID.UUIDString(),
		DocumentID: doc.ID.UUIDString(),
		Content:    content,
		Metadata:   doc.Metadata,
		ChunkDefaults: map[string]string{
			"source": doc.Source,
			"title":  doc.Title,
		},
		Chunker:    tenant.Config.Chunker,
		Hybrid:     tenant.Config.HybridEnabled,
		Contextual: tenant.Config.ContextualRetrievalEnabled,
		Model:      tenant.Config.LLMModel,
	}, func(chunks []ragcore.IngestedChunk) error {
		docChunks := ingestedToDocumentChunks(chunks, doc.TenantID, doc.ID)
		if err := s.docRepo.CreateChunks(ctx, docChunks); err != nil {
			return fmt.Errorf("failed to store chunks: %w", err)
		}
		return nil
	})
	if err != nil {
		s.markDocumentFailed(ctx, doc, err.Error())
		return
	}

	// Mark document as ready
	doc.Status = "READY"
	doc.ChunkCount = len(ingested)
	doc.UpdatedAt = time.Now()
	if err := s.docRepo.Update(ctx, doc); err != nil {
		slog.Error("failed to mark document ready", "error", err, "doc_id", doc.ID)
	}

	// Update tenant usage
	if err := s.tenantRepo.UpdateUsage(ctx, doc.TenantID, repository.TenantUsage{
		DocumentCount: 1, // Increment
		ChunkCount:    len(ingested),
	}); err != nil {
		slog.Warn("failed to update tenant usage", "error", err, "tenant_id", doc.TenantID)
	}
}

// processURL fetches a URL and processes its content
func (s *DocumentService) processURL(ctx context.Context, doc *repository.Document, rawURL string, _ bool, tenant *repository.Tenant) {
	// Update status to PROCESSING
	doc.Status = "PROCESSING"
	doc.UpdatedAt = time.Now()
	if err := s.docRepo.Update(ctx, doc); err != nil {
		slog.Error("failed to update document status", "error", err, "doc_id", doc.ID)
	}

	// Note: useHeadless is ignored - for JS-heavy sites, use the standalone Playwright crawler
	// and submit content via IngestDocument instead

	// Fetch URL content with simple HTTP GET
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL, nil)
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

	const maxURLContentSize = 10 * 1024 * 1024 // 10MB
	limitedReader := io.LimitReader(resp.Body, maxURLContentSize+1)
	body, err := io.ReadAll(limitedReader)
	if err != nil {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("failed to read response: %v", err))
		return
	}
	if len(body) > maxURLContentSize {
		s.markDocumentFailed(ctx, doc, fmt.Sprintf("content too large: exceeds %d bytes", maxURLContentSize))
		return
	}

	content := string(body)

	// Extract title from HTML if present
	title := extractTitle(content)
	if title != "" {
		doc.Title = title
	} else {
		doc.Title = rawURL
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
	// Remove script and style elements (RE2 has no backreferences, so each
	// element gets its own pattern)
	text := regexp.MustCompile(`(?is)<script[^>]*>.*?</script>`).ReplaceAllString(html, "")
	text = regexp.MustCompile(`(?is)<style[^>]*>.*?</style>`).ReplaceAllString(text, "")

	// Remove all HTML tags
	re := regexp.MustCompile(`<[^>]+>`)
	text = re.ReplaceAllString(text, " ")

	// Clean up whitespace
	re = regexp.MustCompile(`\s+`)
	text = re.ReplaceAllString(text, " ")

	return strings.TrimSpace(text)
}

// markDocumentFailed marks a document as failed with an error message
func (s *DocumentService) markDocumentFailed(ctx context.Context, doc *repository.Document, errorMsg string) {
	slog.Error("document processing failed", "doc_id", doc.ID, "error", errorMsg)
	doc.Status = "FAILED"
	doc.ErrorMessage = errorMsg
	doc.UpdatedAt = time.Now()
	if err := s.docRepo.Update(ctx, doc); err != nil {
		slog.Error("failed to mark document as failed", "error", err, "doc_id", doc.ID)
	}
}

// hashContent generates a SHA-256 hash of content
func hashContent(content string) string {
	hash := sha256.Sum256([]byte(content))
	return hex.EncodeToString(hash[:])
}

// documentToProto converts a repository Document to proto Document
func (s *DocumentService) documentToProto(doc *repository.Document) *ragv1.Document {
	return &ragv1.Document{
		Id:           ids.Format(doc.ID),
		TenantId:     ids.Format(doc.TenantID),
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
		Id:         ids.Format(chunk.ID),
		DocumentId: ids.Format(chunk.DocumentID),
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
