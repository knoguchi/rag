package ragcore

import (
	"context"
	"log/slog"
	"strings"
	"time"

	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/memory"
)

// DefaultRewriteTimeout bounds the rewrite LLM call; on timeout the raw
// query is used unchanged.
const DefaultRewriteTimeout = 10 * time.Second

// Rewriter condenses a follow-up question plus conversation history into a
// standalone search query, so retrieval works across conversation turns
// ("what about its pricing?" -> "Acme Cloud pricing").
type Rewriter struct {
	llm     llm.LLM
	timeout time.Duration
}

// NewRewriter creates a query rewriter using the given LLM.
func NewRewriter(llmClient llm.LLM, timeout time.Duration) *Rewriter {
	if timeout <= 0 {
		timeout = DefaultRewriteTimeout
	}
	return &Rewriter{llm: llmClient, timeout: timeout}
}

// Rewrite returns a standalone version of query given the conversation
// history. It returns the query unchanged when there is no history or when
// the rewrite fails or produces implausible output — rewriting must never
// make a query worse than not rewriting.
func (r *Rewriter) Rewrite(ctx context.Context, model string, history []memory.Message, query string) string {
	if len(history) == 0 {
		return query
	}

	// Cap history to the most recent exchanges
	if len(history) > 6 {
		history = history[len(history)-6:]
	}

	var sb strings.Builder
	sb.WriteString("Given the conversation below and a follow-up question, rewrite the follow-up question as a single fully standalone search query that includes any entities it refers to. Output ONLY the rewritten query, nothing else.\n\n## Conversation\n")
	sb.WriteString(memory.FormatForPrompt(history))
	sb.WriteString("\n## Follow-up question\n")
	sb.WriteString(query)
	sb.WriteString("\n\n## Standalone query\n")

	ctx, cancel := context.WithTimeout(ctx, r.timeout)
	defer cancel()

	out, err := r.llm.Generate(ctx, sb.String(), llm.GenerateOptions{
		Model:       model,
		Temperature: 0,
		MaxTokens:   128,
	})
	if err != nil {
		slog.Debug("query rewrite failed, using raw query", "error", err)
		return query
	}

	rewritten := strings.TrimSpace(strings.Trim(strings.TrimSpace(out), `"`))
	// Reject empty, multi-line, or implausibly long rewrites
	if rewritten == "" || strings.ContainsRune(rewritten, '\n') || len(rewritten) > 4*len(query)+128 {
		slog.Debug("query rewrite rejected", "rewritten", rewritten)
		return query
	}

	slog.Debug("query rewritten for retrieval", "original", query, "rewritten", rewritten)
	return rewritten
}
