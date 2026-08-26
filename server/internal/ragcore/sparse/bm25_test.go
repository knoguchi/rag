package sparse

import (
	"reflect"
	"testing"
)

func TestVectorize_Deterministic(t *testing.T) {
	v := New()
	text := "Qdrant supports hybrid search with sparse vectors and RRF fusion."

	a := v.Vectorize(text)
	b := v.Vectorize(text)

	if !reflect.DeepEqual(a, b) {
		t.Error("expected identical vectors for identical input")
	}
	if len(a.Indices) == 0 {
		t.Fatal("expected non-empty vector")
	}
	if len(a.Indices) != len(a.Values) {
		t.Errorf("indices/values length mismatch: %d vs %d", len(a.Indices), len(a.Values))
	}
	for i := 1; i < len(a.Indices); i++ {
		if a.Indices[i] <= a.Indices[i-1] {
			t.Error("expected strictly ascending indices")
		}
	}
}

func TestVectorize_StopwordsAndShortTokens(t *testing.T) {
	v := New()

	if got := v.Vectorize("the and of to a I"); len(got.Indices) != 0 {
		t.Errorf("expected empty vector for stopwords-only input, got %d terms", len(got.Indices))
	}
	if got := v.Vectorize(""); len(got.Indices) != 0 {
		t.Error("expected empty vector for empty input")
	}
}

func TestVectorize_TFSaturation(t *testing.T) {
	v := New()

	// A term appearing more often gets a higher weight...
	once := v.Vectorize("database")
	thrice := v.Vectorize("database database database")
	if len(once.Values) != 1 || len(thrice.Values) != 1 {
		t.Fatal("expected single-term vectors")
	}
	if thrice.Values[0] <= once.Values[0] {
		t.Errorf("expected TF monotonicity: %f <= %f", thrice.Values[0], once.Values[0])
	}

	// ...but the weight saturates below k1+1
	many := v.Vectorize(repeat("database", 1000))
	if many.Values[0] >= defaultK1+1 {
		t.Errorf("expected saturation below k1+1=%f, got %f", defaultK1+1, many.Values[0])
	}
}

func TestVectorizeQuery_UnitWeights(t *testing.T) {
	v := New()

	q := v.VectorizeQuery("database database performance")
	if len(q.Values) != 2 {
		t.Fatalf("expected 2 unique terms, got %d", len(q.Values))
	}
	for _, val := range q.Values {
		if val != 1.0 {
			t.Errorf("expected query weight 1.0, got %f", val)
		}
	}
}

func TestVectorize_QueryAndDocShareIndices(t *testing.T) {
	v := New()

	doc := v.Vectorize("kubernetes deployment")
	query := v.VectorizeQuery("kubernetes")
	if len(query.Indices) != 1 {
		t.Fatal("expected one query term")
	}

	found := false
	for _, idx := range doc.Indices {
		if idx == query.Indices[0] {
			found = true
		}
	}
	if !found {
		t.Error("expected query term index to match document term index")
	}
}

func TestTokenize_CaseAndPunctuation(t *testing.T) {
	v := New()

	a := v.VectorizeQuery("PostgreSQL!")
	b := v.VectorizeQuery("postgresql")
	if !reflect.DeepEqual(a, b) {
		t.Error("expected case- and punctuation-insensitive tokenization")
	}
}

func repeat(word string, n int) string {
	out := make([]byte, 0, (len(word)+1)*n)
	for i := 0; i < n; i++ {
		out = append(out, word...)
		out = append(out, ' ')
	}
	return string(out)
}
