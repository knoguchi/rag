package ragcore

import (
	"context"
	"errors"
	"testing"

	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// fakeEmbedder records the texts it embeds.
type fakeEmbedder struct {
	embedded []string
}

func (f *fakeEmbedder) Embed(ctx context.Context, text string) ([]float32, error) {
	f.embedded = append(f.embedded, text)
	return []float32{0.1}, nil
}

func (f *fakeEmbedder) EmbedBatch(ctx context.Context, texts []string) ([][]float32, error) {
	out := make([][]float32, len(texts))
	for i, t := range texts {
		f.embedded = append(f.embedded, t)
		out[i] = []float32{0.1}
	}
	return out, nil
}

func (f *fakeEmbedder) Dimension() int    { return 1 }
func (f *fakeEmbedder) ModelName() string { return "fake" }

// fakeStore records upserted chunks.
type fakeStore struct {
	vectorstore.VectorStore
	upserted []vectorstore.Chunk
}

func (f *fakeStore) Upsert(ctx context.Context, namespace string, chunks []vectorstore.Chunk) error {
	f.upserted = append(f.upserted, chunks...)
	return nil
}

func TestIngest_ContextualOff_NoLLMCalls(t *testing.T) {
	llm := &fakeLLM{response: "situating context"}
	emb := &fakeEmbedder{}
	store := &fakeStore{}
	e := New(emb, llm, store)
	defer e.Close()

	_, err := e.Ingest(context.Background(), IngestInput{
		Namespace:  "ns",
		DocumentID: "doc1",
		Content:    "Some document content that will be chunked for the test.",
		Contextual: false,
	}, nil)
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if llm.calls != 0 {
		t.Errorf("expected no LLM calls with contextual off, got %d", llm.calls)
	}
}

func TestIngest_ContextualOn_EmbedsContextWithContent(t *testing.T) {
	llm := &fakeLLM{response: "This chunk describes the test document."}
	emb := &fakeEmbedder{}
	store := &fakeStore{}
	e := New(emb, llm, store)
	defer e.Close()

	chunks, err := e.Ingest(context.Background(), IngestInput{
		Namespace:  "ns",
		DocumentID: "doc1",
		Content:    "Some document content that will be chunked for the test.",
		Contextual: true,
	}, nil)
	if err != nil {
		t.Fatalf("ingest failed: %v", err)
	}
	if llm.calls == 0 {
		t.Fatal("expected LLM situating calls with contextual on")
	}
	for _, c := range chunks {
		if c.Metadata["context"] != "This chunk describes the test document." {
			t.Errorf("expected situating context in chunk metadata, got %q", c.Metadata["context"])
		}
	}
	if len(emb.embedded) == 0 {
		t.Fatal("expected embeddings")
	}
	for _, text := range emb.embedded {
		if text[:len("This chunk describes")] != "This chunk describes" {
			t.Errorf("expected embedded text to start with situating context, got %q", text)
		}
	}
	for _, c := range store.upserted {
		if c.Metadata["context"] == "" {
			t.Error("expected context in vector payload")
		}
		// User-facing content stays the original chunk text
		if c.Content == "" || c.Content[:len("This chunk describes")] == "This chunk describes" {
			t.Errorf("expected payload content to be the original chunk, got %q", c.Content)
		}
	}
}

func TestIngest_ContextualLLMFailure_FallsBackToPlainChunk(t *testing.T) {
	llm := &fakeLLM{err: errors.New("llm down")}
	emb := &fakeEmbedder{}
	store := &fakeStore{}
	e := New(emb, llm, store)
	defer e.Close()

	chunks, err := e.Ingest(context.Background(), IngestInput{
		Namespace:  "ns",
		DocumentID: "doc1",
		Content:    "Some document content that will be chunked for the test.",
		Contextual: true,
	}, nil)
	if err != nil {
		t.Fatalf("expected ingest to succeed despite contextualization failure, got %v", err)
	}
	for _, c := range chunks {
		if c.Metadata["context"] != "" {
			t.Error("expected no context after LLM failure")
		}
	}
	if len(store.upserted) == 0 {
		t.Error("expected chunks upserted despite contextualization failure")
	}
}
