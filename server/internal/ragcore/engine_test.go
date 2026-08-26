package ragcore

import (
	"context"
	"strings"
	"testing"

	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// searchRecorder counts dense vs hybrid search calls.
type searchRecorder struct {
	vectorstore.VectorStore
	searchCalls int
	hybridCalls int
}

func (s *searchRecorder) Search(ctx context.Context, ns string, v []float32, topK int, minScore float32) ([]vectorstore.SearchResult, error) {
	s.searchCalls++
	return []vectorstore.SearchResult{{ID: "c1", DocumentID: "d1", Content: "chunk content", Score: 0.9}}, nil
}

func (s *searchRecorder) HybridSearch(ctx context.Context, ns string, dense []float32, sparse *vectorstore.SparseVector, topK int, minScore float32) ([]vectorstore.SearchResult, error) {
	s.hybridCalls++
	return []vectorstore.SearchResult{{ID: "c1", DocumentID: "d1", Content: "chunk content", Score: 0.5}}, nil
}

type staticSparse struct{}

func (staticSparse) Vectorize(string) *vectorstore.SparseVector {
	return &vectorstore.SparseVector{Indices: []uint32{1}, Values: []float32{1}}
}
func (staticSparse) VectorizeQuery(string) *vectorstore.SparseVector {
	return &vectorstore.SparseVector{Indices: []uint32{1}, Values: []float32{1}}
}

func newTestEngine(store vectorstore.VectorStore, l *fakeLLM, opts ...Option) *Engine {
	return New(&fakeEmbedder{}, l, store, opts...)
}

func TestQuery_HybridRouting(t *testing.T) {
	cases := []struct {
		name       string
		hybridOpt  bool
		withSparse bool
		wantHybrid bool
	}{
		{"hybrid enabled with vectorizer", true, true, true},
		{"hybrid disabled", false, true, false},
		{"hybrid enabled without vectorizer", true, false, false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			store := &searchRecorder{}
			var opts []Option
			if tc.withSparse {
				opts = append(opts, WithSparseVectorizer(staticSparse{}))
			}
			e := newTestEngine(store, &fakeLLM{response: "answer"}, opts...)
			defer e.Close()

			_, err := e.Query(context.Background(), "ns", "question", Options{TopK: 2, HybridEnabled: tc.hybridOpt})
			if err != nil {
				t.Fatalf("query failed: %v", err)
			}
			if tc.wantHybrid && (store.hybridCalls != 1 || store.searchCalls != 0) {
				t.Errorf("expected hybrid search, got hybrid=%d dense=%d", store.hybridCalls, store.searchCalls)
			}
			if !tc.wantHybrid && (store.hybridCalls != 0 || store.searchCalls != 1) {
				t.Errorf("expected dense search, got hybrid=%d dense=%d", store.hybridCalls, store.searchCalls)
			}
		})
	}
}

func TestQuery_MemoryIsNamespaceScoped(t *testing.T) {
	llm := &fakeLLM{response: "the answer"}
	e := newTestEngine(&searchRecorder{}, llm)
	defer e.Close()

	opts := Options{TopK: 2, SessionID: "shared-session"}

	// First turn in namespace A establishes history for (A, shared-session)
	if _, err := e.Query(context.Background(), "tenant-a", "first question", opts); err != nil {
		t.Fatal(err)
	}

	// Same session ID in namespace B must see no history
	if _, err := e.Query(context.Background(), "tenant-b", "second question", opts); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(llm.prompts[len(llm.prompts)-1], "Conversation History") {
		t.Error("namespace B saw conversation history from namespace A")
	}

	// Namespace A does see its own history
	if _, err := e.Query(context.Background(), "tenant-a", "follow-up", opts); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(llm.prompts[len(llm.prompts)-1], "Conversation History") {
		t.Error("namespace A lost its own conversation history")
	}
}
