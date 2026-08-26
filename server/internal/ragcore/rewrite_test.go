package ragcore

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/knoguchi/rag/internal/ragcore/llm"
	"github.com/knoguchi/rag/internal/ragcore/memory"
)

// fakeLLM returns a canned response or error from Generate.
type fakeLLM struct {
	response string
	err      error
	calls    int
}

func (f *fakeLLM) Generate(ctx context.Context, prompt string, opts llm.GenerateOptions) (string, error) {
	f.calls++
	return f.response, f.err
}

func (f *fakeLLM) GenerateStream(ctx context.Context, prompt string, opts llm.GenerateOptions) (<-chan llm.StreamChunk, error) {
	ch := make(chan llm.StreamChunk)
	close(ch)
	return ch, nil
}

var testHistory = []memory.Message{
	{Role: "user", Content: "Tell me about Acme Cloud", Timestamp: time.Now()},
	{Role: "assistant", Content: "Acme Cloud is a hosting platform.", Timestamp: time.Now()},
}

func TestRewrite_NoHistoryPassthrough(t *testing.T) {
	f := &fakeLLM{response: "should not be used"}
	r := NewRewriter(f, time.Second)

	got := r.Rewrite(context.Background(), "m", nil, "what is pricing?")
	if got != "what is pricing?" {
		t.Errorf("expected passthrough, got %q", got)
	}
	if f.calls != 0 {
		t.Error("expected no LLM call without history")
	}
}

func TestRewrite_UsesLLMOutput(t *testing.T) {
	f := &fakeLLM{response: "Acme Cloud pricing\n"}
	r := NewRewriter(f, time.Second)

	got := r.Rewrite(context.Background(), "m", testHistory, "what about its pricing?")
	if got != "Acme Cloud pricing" {
		t.Errorf("expected rewritten query, got %q", got)
	}
}

func TestRewrite_ErrorFallsBack(t *testing.T) {
	f := &fakeLLM{err: errors.New("llm down")}
	r := NewRewriter(f, time.Second)

	got := r.Rewrite(context.Background(), "m", testHistory, "what about its pricing?")
	if got != "what about its pricing?" {
		t.Errorf("expected raw query on error, got %q", got)
	}
}

func TestRewrite_RejectsImplausibleOutput(t *testing.T) {
	cases := map[string]string{
		"empty":     "",
		"multiline": "line one\nline two",
		"oversized": strings.Repeat("word ", 200),
	}
	for name, response := range cases {
		t.Run(name, func(t *testing.T) {
			r := NewRewriter(&fakeLLM{response: response}, time.Second)
			got := r.Rewrite(context.Background(), "m", testHistory, "short q")
			if got != "short q" {
				t.Errorf("expected raw query, got %q", got)
			}
		})
	}
}
