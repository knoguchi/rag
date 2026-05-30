package service

import (
	"math"
	"testing"

	"github.com/knoguchi/rag/internal/vectorstore"
)

func TestJaccardSimilarity_Identical(t *testing.T) {
	set := map[string]struct{}{"foo": {}, "bar": {}, "baz": {}}
	sim := jaccardSimilarity(set, set)
	if sim != 1.0 {
		t.Errorf("expected 1.0 for identical sets, got %f", sim)
	}
}

func TestJaccardSimilarity_Disjoint(t *testing.T) {
	set1 := map[string]struct{}{"foo": {}, "bar": {}}
	set2 := map[string]struct{}{"baz": {}, "qux": {}}
	sim := jaccardSimilarity(set1, set2)
	if sim != 0.0 {
		t.Errorf("expected 0.0 for disjoint sets, got %f", sim)
	}
}

func TestJaccardSimilarity_Partial(t *testing.T) {
	set1 := map[string]struct{}{"foo": {}, "bar": {}, "baz": {}}
	set2 := map[string]struct{}{"bar": {}, "baz": {}, "qux": {}}
	// intersection=2 (bar, baz), union=4 (foo, bar, baz, qux)
	sim := jaccardSimilarity(set1, set2)
	expected := 0.5
	if math.Abs(sim-expected) > 0.001 {
		t.Errorf("expected %f, got %f", expected, sim)
	}
}

func TestJaccardSimilarity_BothEmpty(t *testing.T) {
	sim := jaccardSimilarity(map[string]struct{}{}, map[string]struct{}{})
	if sim != 1.0 {
		t.Errorf("expected 1.0 for both empty, got %f", sim)
	}
}

func TestJaccardSimilarity_OneEmpty(t *testing.T) {
	set1 := map[string]struct{}{"foo": {}}
	sim := jaccardSimilarity(set1, map[string]struct{}{})
	if sim != 0.0 {
		t.Errorf("expected 0.0 when one set is empty, got %f", sim)
	}
}

func TestTokenize(t *testing.T) {
	content := "Hello, World! This is a test."
	tokens := tokenize(content)

	// "a" and "is" are <= 2 chars and should be skipped
	if _, ok := tokens["hello"]; !ok {
		t.Error("expected 'hello' in tokens")
	}
	if _, ok := tokens["world"]; !ok {
		t.Error("expected 'world' in tokens")
	}
	if _, ok := tokens["this"]; !ok {
		t.Error("expected 'this' in tokens")
	}
	if _, ok := tokens["test"]; !ok {
		t.Error("expected 'test' in tokens")
	}
	if _, ok := tokens["a"]; ok {
		t.Error("expected 'a' to be filtered out")
	}
}

func TestDeduplicateResults_NoDuplicates(t *testing.T) {
	results := []vectorstore.SearchResult{
		{Content: "the quick brown fox jumps over the lazy dog"},
		{Content: "completely different content about programming languages"},
	}
	deduped := deduplicateResults(results, 0.7)
	if len(deduped) != 2 {
		t.Errorf("expected 2 results, got %d", len(deduped))
	}
}

func TestDeduplicateResults_WithDuplicates(t *testing.T) {
	results := []vectorstore.SearchResult{
		{Content: "the quick brown fox jumps over the lazy dog", Score: 0.9},
		{Content: "the quick brown fox jumps over the lazy cat", Score: 0.8},
	}
	deduped := deduplicateResults(results, 0.7)
	if len(deduped) != 1 {
		t.Errorf("expected 1 result after dedup, got %d", len(deduped))
	}
	if deduped[0].Score != 0.9 {
		t.Error("expected higher-scored result to be kept")
	}
}

func TestDeduplicateResults_SingleResult(t *testing.T) {
	results := []vectorstore.SearchResult{
		{Content: "single result"},
	}
	deduped := deduplicateResults(results, 0.7)
	if len(deduped) != 1 {
		t.Errorf("expected 1 result, got %d", len(deduped))
	}
}

func TestDeduplicateResults_Empty(t *testing.T) {
	deduped := deduplicateResults(nil, 0.7)
	if len(deduped) != 0 {
		t.Errorf("expected 0 results, got %d", len(deduped))
	}
}
