package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	ragv1 "github.com/knoguchi/rag/gen/rag/v1"
	"github.com/knoguchi/rag/internal/embedder"
	"github.com/knoguchi/rag/internal/llm"
	"github.com/knoguchi/rag/internal/memory"
	"github.com/knoguchi/rag/internal/reranker"
	"github.com/knoguchi/rag/internal/repository"
	"github.com/knoguchi/rag/internal/vectorstore"
	"google.golang.org/grpc"
	"google.golang.org/grpc/codes"
	"google.golang.org/grpc/status"
)

// RAGService implements ragv1.RAGServiceServer
type RAGService struct {
	ragv1.UnimplementedRAGServiceServer

	tenantRepo  repository.TenantRepository
	docRepo     repository.DocumentRepository
	embedder    embedder.Embedder
	vectorDB    vectorstore.VectorStore
	llmClient   llm.LLM
	reranker    reranker.Reranker // Optional: if set, results will be reranked
	useHybrid   bool              // If true, uses hybrid search (dense + sparse)
	sparseModel SparseVectorizer  // Optional: converts text to sparse vectors
	memory      *memory.Store     // Conversation memory for session-based context
}

// SparseVectorizer converts text to sparse vectors for hybrid search
type SparseVectorizer interface {
	Vectorize(text string) *vectorstore.SparseVector
}

// RAGServiceOption is a functional option for configuring RAGService.
type RAGServiceOption func(*RAGService)

// WithReranker sets a reranker for the RAG service.
func WithReranker(r reranker.Reranker) RAGServiceOption {
	return func(s *RAGService) {
		s.reranker = r
	}
}

// WithHybridSearch enables hybrid search with the given sparse vectorizer.
func WithHybridSearch(sparseModel SparseVectorizer) RAGServiceOption {
	return func(s *RAGService) {
		s.useHybrid = true
		s.sparseModel = sparseModel
	}
}

// NewRAGService creates a new RAGService
func NewRAGService(
	tenantRepo repository.TenantRepository,
	docRepo repository.DocumentRepository,
	embedder embedder.Embedder,
	vectorDB vectorstore.VectorStore,
	llmClient llm.LLM,
	opts ...RAGServiceOption,
) *RAGService {
	s := &RAGService{
		tenantRepo: tenantRepo,
		docRepo:    docRepo,
		embedder:   embedder,
		vectorDB:   vectorDB,
		llmClient:  llmClient,
		memory:     memory.DefaultStore(), // Initialize conversation memory
	}

	for _, opt := range opts {
		opt(s)
	}

	return s
}

// Query retrieves context and generates an LLM response
func (s *RAGService) Query(ctx context.Context, req *ragv1.QueryRequest) (*ragv1.QueryResponse, error) {
	startTime := time.Now()

	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	// Get tenant config
	tenant, err := s.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "tenant not found: %v", err)
	}

	// Build query options from tenant config and request options
	options := s.buildQueryOptions(tenant, req.Options)

	// Step 1: Embed the query
	retrievalStart := time.Now()
	queryVector, err := s.embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to embed query: %v", err)
	}

	// Step 2: Search for relevant chunks
	var searchResults []vectorstore.SearchResult

	if s.useHybrid && s.sparseModel != nil {
		// Use hybrid search (combines dense + sparse vectors with RRF)
		sparseVector := s.sparseModel.Vectorize(req.Query)
		searchResults, err = s.vectorDB.HybridSearch(ctx, tenantID.String(), queryVector, sparseVector, options.topK*3, options.minScore)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to perform hybrid search: %v", err)
		}
	} else {
		// Dense vector-only search (retrieve extra for deduplication and reranking)
		searchResults, err = s.vectorDB.Search(ctx, tenantID.String(), queryVector, options.topK*3, options.minScore)
		if err != nil {
			return nil, status.Errorf(codes.Internal, "failed to search vectors: %v", err)
		}
	}

	// Step 2.5: Deduplicate similar chunks (70% Jaccard threshold)
	searchResults = deduplicateResults(searchResults, 0.7)

	// Step 2.6: Rerank if enabled for this tenant
	if s.reranker != nil && tenant.Config.RerankerEnabled && len(searchResults) > 0 {
		reranked, err := s.reranker.Rerank(ctx, req.Query, searchResults, options.topK)
		if err == nil && len(reranked) > 0 {
			// Convert reranked results back to search results with updated scores
			searchResults = make([]vectorstore.SearchResult, len(reranked))
			for i, r := range reranked {
				searchResults[i] = r.SearchResult
				searchResults[i].Score = r.RerankerScore // Use reranker score
			}
		}
		// On error, continue with original results
	}

	// Limit to topK after deduplication/reranking
	if len(searchResults) > options.topK {
		searchResults = searchResults[:options.topK]
	}
	retrievalTime := time.Since(retrievalStart)

	// Convert search results to retrieved chunks
	sources := make([]*ragv1.RetrievedChunk, len(searchResults))
	chunkContexts := make([]chunkContext, len(searchResults))
	for i, result := range searchResults {
		sources[i] = &ragv1.RetrievedChunk{
			DocumentId: result.DocumentID,
			ChunkId:    result.ID,
			Content:    result.Content,
			Score:      result.Score,
			Source:     result.Metadata["source"],
			Title:      result.Metadata["title"],
			Metadata:   result.Metadata,
		}
		chunkContexts[i] = chunkContext{
			Content:  result.Content,
			Source:   result.Metadata["source"],
			Title:    result.Metadata["title"],
			Score:    result.Score,
			Metadata: result.Metadata,
		}
	}

	// Step 3: Get conversation history if session ID provided
	var history []memory.Message
	if req.SessionId != "" {
		history = s.memory.GetRecentHistory(req.SessionId, 10) // Last 10 messages (5 turns)
		s.memory.AddUserMessage(req.SessionId, req.Query)
	}

	// Step 4: Build prompt and call LLM
	generationStart := time.Now()
	prompt := s.buildRAGPrompt(options.systemPrompt, chunkContexts, req.Query, history)

	llmOpts := llm.GenerateOptions{
		Model:        options.model,
		SystemPrompt: options.systemPrompt,
		Temperature:  options.temperature,
		MaxTokens:    options.maxTokens,
	}

	answer, err := s.llmClient.Generate(ctx, prompt, llmOpts)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to generate response: %v", err)
	}
	generationTime := time.Since(generationStart)

	// Store assistant response in memory
	if req.SessionId != "" {
		s.memory.AddAssistantMessage(req.SessionId, answer)
	}

	totalTime := time.Since(startTime)

	return &ragv1.QueryResponse{
		Answer:  answer,
		Sources: sources,
		Metadata: &ragv1.QueryMetadata{
			RetrievalTimeMs:  retrievalTime.Milliseconds(),
			GenerationTimeMs: generationTime.Milliseconds(),
			TotalTimeMs:      totalTime.Milliseconds(),
			ChunksRetrieved:  int32(len(sources)),
			Model:            options.model,
			PromptTokens:     0, // TODO: Implement token counting
			CompletionTokens: 0, // TODO: Implement token counting
		},
	}, nil
}

// QueryStream streams the LLM response for interactive use
func (s *RAGService) QueryStream(req *ragv1.QueryRequest, stream grpc.ServerStreamingServer[ragv1.QueryStreamResponse]) error {
	ctx := stream.Context()
	startTime := time.Now()

	if req.TenantId == "" {
		return status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.Query == "" {
		return status.Error(codes.InvalidArgument, "query is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	// Get tenant config
	tenant, err := s.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		return status.Errorf(codes.NotFound, "tenant not found: %v", err)
	}

	// Build query options from tenant config and request options
	options := s.buildQueryOptions(tenant, req.Options)

	// Step 1: Embed the query
	retrievalStart := time.Now()
	queryVector, err := s.embedder.Embed(ctx, req.Query)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to embed query: %v", err)
	}

	// Step 2: Search for relevant chunks
	var searchResults []vectorstore.SearchResult

	if s.useHybrid && s.sparseModel != nil {
		// Use hybrid search (combines dense + sparse vectors with RRF)
		sparseVector := s.sparseModel.Vectorize(req.Query)
		searchResults, err = s.vectorDB.HybridSearch(ctx, tenantID.String(), queryVector, sparseVector, options.topK*3, options.minScore)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to perform hybrid search: %v", err)
		}
	} else {
		// Dense vector-only search (retrieve extra for deduplication and reranking)
		searchResults, err = s.vectorDB.Search(ctx, tenantID.String(), queryVector, options.topK*3, options.minScore)
		if err != nil {
			return status.Errorf(codes.Internal, "failed to search vectors: %v", err)
		}
	}

	// Step 2.5: Deduplicate similar chunks (70% Jaccard threshold)
	searchResults = deduplicateResults(searchResults, 0.7)

	// Step 2.6: Rerank if enabled for this tenant
	if s.reranker != nil && tenant.Config.RerankerEnabled && len(searchResults) > 0 {
		reranked, err := s.reranker.Rerank(ctx, req.Query, searchResults, options.topK)
		if err == nil && len(reranked) > 0 {
			// Convert reranked results back to search results with updated scores
			searchResults = make([]vectorstore.SearchResult, len(reranked))
			for i, r := range reranked {
				searchResults[i] = r.SearchResult
				searchResults[i].Score = r.RerankerScore // Use reranker score
			}
		}
		// On error, continue with original results
	}

	// Limit to topK after deduplication/reranking
	if len(searchResults) > options.topK {
		searchResults = searchResults[:options.topK]
	}
	retrievalTime := time.Since(retrievalStart)

	// Step 3: Stream sources first
	chunkContexts := make([]chunkContext, len(searchResults))
	for i, result := range searchResults {
		source := &ragv1.RetrievedChunk{
			DocumentId: result.DocumentID,
			ChunkId:    result.ID,
			Content:    result.Content,
			Score:      result.Score,
			Source:     result.Metadata["source"],
			Title:      result.Metadata["title"],
			Metadata:   result.Metadata,
		}

		if err := stream.Send(&ragv1.QueryStreamResponse{
			Event: &ragv1.QueryStreamResponse_Source{Source: source},
		}); err != nil {
			return err
		}

		chunkContexts[i] = chunkContext{
			Content:  result.Content,
			Source:   result.Metadata["source"],
			Title:    result.Metadata["title"],
			Score:    result.Score,
			Metadata: result.Metadata,
		}
	}

	// Step 4: Get conversation history if session ID provided
	var history []memory.Message
	if req.SessionId != "" {
		history = s.memory.GetRecentHistory(req.SessionId, 10) // Last 10 messages (5 turns)
		s.memory.AddUserMessage(req.SessionId, req.Query)
	}

	// Step 5: Build prompt and stream LLM response
	generationStart := time.Now()
	prompt := s.buildRAGPrompt(options.systemPrompt, chunkContexts, req.Query, history)

	llmOpts := llm.GenerateOptions{
		Model:        options.model,
		SystemPrompt: options.systemPrompt,
		Temperature:  options.temperature,
		MaxTokens:    options.maxTokens,
	}

	tokenChan, err := s.llmClient.GenerateStream(ctx, prompt, llmOpts)
	if err != nil {
		return status.Errorf(codes.Internal, "failed to start streaming: %v", err)
	}

	// Collect full response for memory
	var fullResponse strings.Builder

	// Stream tokens
	for chunk := range tokenChan {
		if chunk.Error != nil {
			// Send error and stop streaming
			if err := stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Error{
					Error: &ragv1.StreamError{
						Code:    "generation_error",
						Message: chunk.Error.Error(),
					},
				},
			}); err != nil {
				return err
			}
			return nil
		}

		if chunk.Token != "" {
			fullResponse.WriteString(chunk.Token) // Collect for memory
			if err := stream.Send(&ragv1.QueryStreamResponse{
				Event: &ragv1.QueryStreamResponse_Token{Token: chunk.Token},
			}); err != nil {
				return err
			}
		}
	}

	// Store assistant response in memory
	if req.SessionId != "" {
		s.memory.AddAssistantMessage(req.SessionId, fullResponse.String())
	}

	generationTime := time.Since(generationStart)
	totalTime := time.Since(startTime)

	// Send final metadata
	if err := stream.Send(&ragv1.QueryStreamResponse{
		Event: &ragv1.QueryStreamResponse_Metadata{
			Metadata: &ragv1.QueryMetadata{
				RetrievalTimeMs:  retrievalTime.Milliseconds(),
				GenerationTimeMs: generationTime.Milliseconds(),
				TotalTimeMs:      totalTime.Milliseconds(),
				ChunksRetrieved:  int32(len(searchResults)),
				Model:            options.model,
				PromptTokens:     0,
				CompletionTokens: 0,
			},
		},
	}); err != nil {
		return err
	}

	return nil
}

// Retrieve only retrieves relevant chunks without LLM generation
func (s *RAGService) Retrieve(ctx context.Context, req *ragv1.RetrieveRequest) (*ragv1.RetrieveResponse, error) {
	startTime := time.Now()

	if req.TenantId == "" {
		return nil, status.Error(codes.InvalidArgument, "tenant_id is required")
	}
	if req.Query == "" {
		return nil, status.Error(codes.InvalidArgument, "query is required")
	}

	tenantID, err := uuid.Parse(req.TenantId)
	if err != nil {
		return nil, status.Error(codes.InvalidArgument, "invalid tenant_id format")
	}

	// Get tenant config
	tenant, err := s.tenantRepo.GetByID(ctx, tenantID)
	if err != nil {
		return nil, status.Errorf(codes.NotFound, "tenant not found: %v", err)
	}

	// Build retrieval options
	topK := tenant.Config.TopK
	minScore := tenant.Config.MinScore

	if req.Options != nil {
		if req.Options.TopK > 0 {
			topK = int(req.Options.TopK)
		}
		if req.Options.MinScore > 0 {
			minScore = req.Options.MinScore
		}
	}

	// Embed the query
	queryVector, err := s.embedder.Embed(ctx, req.Query)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to embed query: %v", err)
	}

	// Search for relevant chunks
	searchResults, err := s.vectorDB.Search(ctx, tenantID.String(), queryVector, topK, minScore)
	if err != nil {
		return nil, status.Errorf(codes.Internal, "failed to search vectors: %v", err)
	}

	// Filter by document IDs if specified
	if req.Options != nil && len(req.Options.DocumentIds) > 0 {
		docIDSet := make(map[string]bool)
		for _, id := range req.Options.DocumentIds {
			docIDSet[id] = true
		}

		var filtered []vectorstore.SearchResult
		for _, result := range searchResults {
			if docIDSet[result.DocumentID] {
				filtered = append(filtered, result)
			}
		}
		searchResults = filtered
	}

	// Convert search results to retrieved chunks
	chunks := make([]*ragv1.RetrievedChunk, len(searchResults))
	for i, result := range searchResults {
		chunks[i] = &ragv1.RetrievedChunk{
			DocumentId: result.DocumentID,
			ChunkId:    result.ID,
			Content:    result.Content,
			Score:      result.Score,
			Source:     result.Metadata["source"],
			Title:      result.Metadata["title"],
			Metadata:   result.Metadata,
		}
	}

	retrievalTime := time.Since(startTime)

	return &ragv1.RetrieveResponse{
		Chunks: chunks,
		Metadata: &ragv1.RetrieveMetadata{
			RetrievalTimeMs:     retrievalTime.Milliseconds(),
			ChunksRetrieved:     int32(len(chunks)),
			TotalChunksSearched: 0, // TODO: Get from vector store if available
		},
	}, nil
}

// queryOptions holds resolved options for a query
type queryOptions struct {
	topK         int
	minScore     float32
	systemPrompt string
	temperature  float32
	maxTokens    int
	model        string
}

// buildQueryOptions builds query options from tenant config and request options
func (s *RAGService) buildQueryOptions(tenant *repository.Tenant, opts *ragv1.QueryOptions) queryOptions {
	options := queryOptions{
		topK:         tenant.Config.TopK,
		minScore:     tenant.Config.MinScore,
		systemPrompt: tenant.Config.SystemPrompt,
		temperature:  0.3, // Low temperature for factual, deterministic RAG responses
		maxTokens:    2048, // Default max tokens
		model:        tenant.Config.LLMModel,
	}

	// Apply defaults if tenant config has zero values
	if options.topK <= 0 {
		options.topK = 4 // Fewer sources = more focused answers
	}
	if options.minScore <= 0 {
		options.minScore = 0.5 // Higher threshold = more relevant results only
	}
	if options.systemPrompt == "" {
		options.systemPrompt = defaultSystemPrompt
	}

	// Override with request options if provided
	if opts != nil {
		if opts.TopK > 0 {
			options.topK = int(opts.TopK)
		}
		if opts.MinScore > 0 {
			options.minScore = opts.MinScore
		}
		if opts.SystemPrompt != "" {
			options.systemPrompt = opts.SystemPrompt
		}
		if opts.Temperature > 0 {
			options.temperature = opts.Temperature
		}
		if opts.MaxTokens > 0 {
			options.maxTokens = int(opts.MaxTokens)
		}
	}

	return options
}

// chunkContext holds chunk content with metadata for prompt building
type chunkContext struct {
	Content  string
	Source   string
	Title    string
	Score    float32
	Metadata map[string]string
}

// buildRAGPrompt constructs the RAG prompt with metadata, conversation history, and chain-of-thought structure
func (s *RAGService) buildRAGPrompt(systemPrompt string, chunks []chunkContext, query string, history []memory.Message) string {
	var sb strings.Builder

	// System instructions
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n")

	// Conversation history (if any)
	if len(history) > 0 {
		sb.WriteString("## Conversation History\n")
		sb.WriteString("(Previous exchanges in this session for context)\n\n")
		sb.WriteString(memory.FormatForPrompt(history))
		sb.WriteString("\n")
	}

	// Context section with metadata (relevance scores omitted to avoid biasing LLM)
	sb.WriteString("## Context Documents\n\n")
	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[Doc %d]", i+1))

		// Add metadata if available
		if chunk.Title != "" {
			sb.WriteString(fmt.Sprintf(" (Title: %s)", chunk.Title))
		}
		if chunk.Source != "" {
			sb.WriteString(fmt.Sprintf(" (Source: %s)", chunk.Source))
		}
		sb.WriteString("\n")
		sb.WriteString(chunk.Content)
		sb.WriteString("\n\n")
	}

	// Question
	sb.WriteString("## Question\n")
	sb.WriteString(query)
	sb.WriteString("\n\n")

	// Direct answer prompt (no chain-of-thought to keep responses concise)
	sb.WriteString("## Answer (be brief and direct)\n")

	return sb.String()
}

// deduplicateResults removes chunks with highly similar content to reduce redundancy.
// It uses Jaccard similarity on word sets with a threshold of 0.7 (70% overlap).
func deduplicateResults(results []vectorstore.SearchResult, threshold float64) []vectorstore.SearchResult {
	if len(results) <= 1 {
		return results
	}

	// Build word sets for each result
	wordSets := make([]map[string]struct{}, len(results))
	for i, result := range results {
		wordSets[i] = tokenize(result.Content)
	}

	// Keep track of which results to include
	keep := make([]bool, len(results))
	for i := range keep {
		keep[i] = true
	}

	// Compare each pair and mark duplicates (keep higher-scored one)
	for i := 0; i < len(results); i++ {
		if !keep[i] {
			continue
		}
		for j := i + 1; j < len(results); j++ {
			if !keep[j] {
				continue
			}
			similarity := jaccardSimilarity(wordSets[i], wordSets[j])
			if similarity >= threshold {
				// Keep the one with higher score (results are typically sorted by score descending)
				// Since i < j and results are sorted, keep[i] stays true, mark j as duplicate
				keep[j] = false
			}
		}
	}

	// Build deduplicated result list
	deduplicated := make([]vectorstore.SearchResult, 0, len(results))
	for i, result := range results {
		if keep[i] {
			deduplicated = append(deduplicated, result)
		}
	}

	return deduplicated
}

// tokenize converts content into a set of lowercase words for similarity comparison.
func tokenize(content string) map[string]struct{} {
	words := strings.Fields(strings.ToLower(content))
	wordSet := make(map[string]struct{}, len(words))
	for _, word := range words {
		// Remove common punctuation
		word = strings.Trim(word, ".,!?;:\"'()[]{}=<>")
		if len(word) > 2 { // Skip very short tokens
			wordSet[word] = struct{}{}
		}
	}
	return wordSet
}

// jaccardSimilarity computes the Jaccard similarity between two word sets.
// Returns a value between 0 (no overlap) and 1 (identical).
func jaccardSimilarity(set1, set2 map[string]struct{}) float64 {
	if len(set1) == 0 && len(set2) == 0 {
		return 1.0
	}
	if len(set1) == 0 || len(set2) == 0 {
		return 0.0
	}

	// Count intersection
	intersection := 0
	for word := range set1 {
		if _, exists := set2[word]; exists {
			intersection++
		}
	}

	// Union = |set1| + |set2| - intersection
	union := len(set1) + len(set2) - intersection

	return float64(intersection) / float64(union)
}
