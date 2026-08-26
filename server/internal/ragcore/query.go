package ragcore

import (
	"context"
	"fmt"
	"time"

	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/memory"
	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// retrieve runs the retrieval half of the pipeline: embed -> (hybrid) search
// -> dedupe -> rerank -> truncate to TopK.
func (e *Engine) retrieve(ctx context.Context, namespace, query string, opts Options) ([]vectorstore.SearchResult, error) {
	queryVector, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	// Retrieve extra results for deduplication and reranking
	var results []vectorstore.SearchResult
	if opts.HybridEnabled && e.sparse != nil {
		sparseVector := e.sparse.VectorizeQuery(query)
		results, err = e.store.HybridSearch(ctx, namespace, queryVector, sparseVector, opts.TopK*3, opts.MinScore)
	} else {
		results, err = e.store.Search(ctx, namespace, queryVector, opts.TopK*3, opts.MinScore)
	}
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	// Deduplicate similar chunks (70% Jaccard threshold)
	results = deduplicateResults(results, 0.7)

	if opts.RerankEnabled && e.reranker != nil && len(results) > 0 {
		reranked, err := e.reranker.Rerank(ctx, query, results, opts.TopK)
		if err == nil && len(reranked) > 0 {
			results = make([]vectorstore.SearchResult, len(reranked))
			for i, r := range reranked {
				results[i] = r.SearchResult
				results[i].Score = r.RerankerScore
			}
		}
		// On error, continue with original vector-score ordering
	}

	if len(results) > opts.TopK {
		results = results[:opts.TopK]
	}
	return results, nil
}

// fetchHistory returns recent conversation history for the session, or nil
// when memory is disabled for this call.
func (e *Engine) fetchHistory(namespace string, opts Options) []memory.Message {
	if opts.SessionID == "" {
		return nil
	}
	return e.memory.GetRecentHistory(sessionKey(namespace, opts.SessionID), 10) // Last 10 messages (5 turns)
}

func (e *Engine) rememberQuestion(namespace string, opts Options, query string) {
	if opts.SessionID == "" {
		return
	}
	e.memory.AddUserMessage(sessionKey(namespace, opts.SessionID), query)
}

// searchQuery returns the query to use for retrieval: the conversation-aware
// rewrite when a rewriter is configured and history exists, else the raw
// query. The original query is always what goes into the final prompt.
func (e *Engine) searchQuery(ctx context.Context, opts Options, history []memory.Message, query string) string {
	if e.rewriter == nil || len(history) == 0 {
		return query
	}
	return e.rewriter.Rewrite(ctx, opts.Model, history, query)
}

func (e *Engine) rememberAnswer(namespace string, opts Options, answer string) {
	if opts.SessionID == "" {
		return
	}
	e.memory.AddAssistantMessage(sessionKey(namespace, opts.SessionID), answer)
}

func toRetrievedChunk(r vectorstore.SearchResult) RetrievedChunk {
	return RetrievedChunk{
		ID:         r.ID,
		DocumentID: r.DocumentID,
		Content:    r.Content,
		Context:    r.Metadata["context"],
		Score:      r.Score,
		Source:     r.Metadata["source"],
		Title:      r.Metadata["title"],
		Metadata:   r.Metadata,
	}
}

// Query runs the full RAG pipeline and returns the generated answer with its
// sources.
func (e *Engine) Query(ctx context.Context, namespace, query string, opts Options) (*QueryResult, error) {
	startTime := time.Now()

	history := e.fetchHistory(namespace, opts)

	retrievalStart := time.Now()
	results, err := e.retrieve(ctx, namespace, e.searchQuery(ctx, opts, history, query), opts)
	if err != nil {
		return nil, err
	}
	retrievalTime := time.Since(retrievalStart)

	sources := make([]RetrievedChunk, len(results))
	for i, r := range results {
		sources[i] = toRetrievedChunk(r)
	}

	e.rememberQuestion(namespace, opts, query)

	generationStart := time.Now()
	prompt := buildRAGPrompt(opts.SystemPrompt, sources, query, history)

	answer, err := e.llm.Generate(ctx, prompt, llm.GenerateOptions{
		Model:        opts.Model,
		SystemPrompt: opts.SystemPrompt,
		Temperature:  opts.Temperature,
		MaxTokens:    opts.MaxTokens,
	})
	if err != nil {
		return nil, fmt.Errorf("failed to generate response: %w", err)
	}
	generationTime := time.Since(generationStart)

	e.rememberAnswer(namespace, opts, answer)

	return &QueryResult{
		Answer:  answer,
		Sources: sources,
		Model:   opts.Model,
		Timings: Timings{
			Retrieval:  retrievalTime,
			Generation: generationTime,
			Total:      time.Since(startTime),
		},
	}, nil
}

// QueryStream runs the full RAG pipeline, emitting sources, tokens, and a
// final metadata event through emit. If emit returns an error, streaming
// stops and that error is returned.
func (e *Engine) QueryStream(ctx context.Context, namespace, query string, opts Options, emit func(StreamEvent) error) error {
	startTime := time.Now()

	history := e.fetchHistory(namespace, opts)

	retrievalStart := time.Now()
	results, err := e.retrieve(ctx, namespace, e.searchQuery(ctx, opts, history, query), opts)
	if err != nil {
		return err
	}
	retrievalTime := time.Since(retrievalStart)

	sources := make([]RetrievedChunk, len(results))
	for i, r := range results {
		sources[i] = toRetrievedChunk(r)
		src := sources[i]
		if err := emit(StreamEvent{Source: &src}); err != nil {
			return err
		}
	}

	e.rememberQuestion(namespace, opts, query)

	generationStart := time.Now()
	prompt := buildRAGPrompt(opts.SystemPrompt, sources, query, history)

	tokenChan, err := e.llm.GenerateStream(ctx, prompt, llm.GenerateOptions{
		Model:        opts.Model,
		SystemPrompt: opts.SystemPrompt,
		Temperature:  opts.Temperature,
		MaxTokens:    opts.MaxTokens,
	})
	if err != nil {
		return fmt.Errorf("failed to start streaming: %w", err)
	}

	var fullResponse []byte
	for chunk := range tokenChan {
		if chunk.Error != nil {
			// Report the generation error to the client and stop.
			return emit(StreamEvent{Err: chunk.Error})
		}
		if chunk.Token != "" {
			fullResponse = append(fullResponse, chunk.Token...)
			if err := emit(StreamEvent{Token: chunk.Token}); err != nil {
				return err
			}
		}
	}

	e.rememberAnswer(namespace, opts, string(fullResponse))

	return emit(StreamEvent{Meta: &QueryResult{
		Sources: sources,
		Model:   opts.Model,
		Timings: Timings{
			Retrieval:  retrievalTime,
			Generation: time.Since(generationStart),
			Total:      time.Since(startTime),
		},
	}})
}

// Retrieve returns relevant chunks without LLM generation. documentIDs, when
// non-empty, filters results to those documents.
func (e *Engine) Retrieve(ctx context.Context, namespace, query string, opts Options, documentIDs []string) (*RetrieveResult, error) {
	startTime := time.Now()

	queryVector, err := e.embedder.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("failed to embed query: %w", err)
	}

	results, err := e.store.Search(ctx, namespace, queryVector, opts.TopK, opts.MinScore)
	if err != nil {
		return nil, fmt.Errorf("search failed: %w", err)
	}

	if len(documentIDs) > 0 {
		docIDSet := make(map[string]bool)
		for _, id := range documentIDs {
			docIDSet[id] = true
		}
		var filtered []vectorstore.SearchResult
		for _, r := range results {
			if docIDSet[r.DocumentID] {
				filtered = append(filtered, r)
			}
		}
		results = filtered
	}

	chunks := make([]RetrievedChunk, len(results))
	for i, r := range results {
		chunks[i] = toRetrievedChunk(r)
	}

	return &RetrieveResult{
		Chunks:    chunks,
		Retrieval: time.Since(startTime),
	}, nil
}
