package ragcore

import (
	"context"
	"log/slog"
	"strings"
	"sync"
	"time"

	"github.com/knoguchi/rag/internal/ragcore/llm"
)

const (
	// contextualDocExcerpt is how much of the document is shown to the LLM
	// when situating a chunk.
	contextualDocExcerpt = 4000
	// contextualTimeout bounds each per-chunk situating call.
	contextualTimeout = 30 * time.Second
	// DefaultContextualConcurrency is the default number of concurrent
	// situating calls (local LLMs handle little parallelism).
	DefaultContextualConcurrency = 2
)

// contextualize generates a short situating context for each chunk
// (Anthropic-style contextual retrieval) and stores it in the chunk's
// "context" metadata key. The context is embedded together with the chunk
// content, which disambiguates chunks that are unclear out of context
// ("the company grew 3%" -> which company? which quarter?).
//
// Failures are per-chunk and non-fatal: a chunk whose call fails is simply
// embedded without context.
func (e *Engine) contextualize(ctx context.Context, docContent, model string, chunks []IngestedChunk) {
	excerpt := docContent
	if len(excerpt) > contextualDocExcerpt {
		excerpt = excerpt[:contextualDocExcerpt]
	}

	concurrency := e.contextualConcurrency
	if concurrency <= 0 {
		concurrency = DefaultContextualConcurrency
	}

	var wg sync.WaitGroup
	sem := make(chan struct{}, concurrency)
	for i := range chunks {
		wg.Add(1)
		go func(c *IngestedChunk) {
			defer wg.Done()
			select {
			case sem <- struct{}{}:
				defer func() { <-sem }()
			case <-ctx.Done():
				return
			}

			situating, err := e.situateChunk(ctx, excerpt, c.Content, model)
			if err != nil {
				slog.Debug("chunk contextualization failed, embedding without context", "error", err)
				return
			}
			if situating != "" {
				if c.Metadata == nil {
					c.Metadata = make(map[string]string)
				}
				c.Metadata["context"] = situating
			}
		}(&chunks[i])
	}
	wg.Wait()
}

// Contextualize generates situating context for chunks that lack one, in
// place. Exposed for reindexing tools; docContent may be an approximation
// (e.g. concatenated chunks) when the original document is unavailable.
func (e *Engine) Contextualize(ctx context.Context, docContent, model string, chunks []IngestedChunk) {
	e.contextualize(ctx, docContent, model, chunks)
}

// situateChunk asks the LLM for 1-2 sentences situating the chunk within the
// document.
func (e *Engine) situateChunk(ctx context.Context, docExcerpt, chunk, model string) (string, error) {
	var sb strings.Builder
	sb.WriteString("<document>\n")
	sb.WriteString(docExcerpt)
	sb.WriteString("\n</document>\n\nHere is a chunk from that document:\n<chunk>\n")
	sb.WriteString(chunk)
	sb.WriteString("\n</chunk>\n\nWrite 1-2 short sentences situating this chunk within the overall document, to improve search retrieval of the chunk. Answer with ONLY the situating context, nothing else.\n")

	ctx, cancel := context.WithTimeout(ctx, contextualTimeout)
	defer cancel()

	out, err := e.llm.Generate(ctx, sb.String(), llm.GenerateOptions{
		Model:       model,
		Temperature: 0,
		MaxTokens:   150,
	})
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(out), nil
}

// embedText returns the text to embed for a chunk: the situating context (if
// any) prepended to the content.
func embedText(c IngestedChunk) string {
	if ctx := c.Metadata["context"]; ctx != "" {
		return ctx + "\n" + c.Content
	}
	return c.Content
}
