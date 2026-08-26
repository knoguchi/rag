// Package sparse provides sparse vectorizers for hybrid (dense + keyword)
// search.
package sparse

import (
	"hash/fnv"
	"sort"
	"strings"
	"unicode"

	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

// BM25 default parameters.
const (
	defaultK1        = 1.2
	defaultB         = 0.75
	defaultAvgDocLen = 200 // words; roughly the chunker's target chunk size
)

// stopwords is a small English stopword set; these terms carry almost no
// retrieval signal and would dominate term frequencies.
var stopwords = map[string]struct{}{
	"a": {}, "an": {}, "and": {}, "are": {}, "as": {}, "at": {}, "be": {},
	"but": {}, "by": {}, "for": {}, "from": {}, "had": {}, "has": {},
	"have": {}, "he": {}, "her": {}, "his": {}, "if": {}, "in": {},
	"into": {}, "is": {}, "it": {}, "its": {}, "of": {}, "on": {},
	"or": {}, "our": {}, "she": {}, "so": {}, "that": {}, "the": {},
	"their": {}, "them": {}, "then": {}, "there": {}, "these": {},
	"they": {}, "this": {}, "to": {}, "was": {}, "we": {}, "were": {},
	"what": {}, "when": {}, "which": {}, "will": {}, "with": {},
	"you": {}, "your": {},
}

// BM25 converts text into sparse vectors using BM25 term weighting.
//
// Document-side values carry only the BM25 term-frequency component; inverse
// document frequency is applied server-side by Qdrant (sparse vector modifier
// IDF), so no corpus statistics are needed here. The average document length
// is a constant approximation rather than a live corpus statistic — the
// resulting scores are useful for ranking but are not calibrated BM25 scores.
type BM25 struct {
	k1        float32
	b         float32
	avgDocLen float32
}

// Option configures a BM25 vectorizer.
type Option func(*BM25)

// WithAvgDocLen overrides the assumed average document length in words.
func WithAvgDocLen(words int) Option {
	return func(v *BM25) {
		if words > 0 {
			v.avgDocLen = float32(words)
		}
	}
}

// New creates a BM25 sparse vectorizer with standard parameters
// (k1=1.2, b=0.75).
func New(opts ...Option) *BM25 {
	v := &BM25{
		k1:        defaultK1,
		b:         defaultB,
		avgDocLen: defaultAvgDocLen,
	}
	for _, opt := range opts {
		opt(v)
	}
	return v
}

// Vectorize produces a document-side sparse vector: for each term, the BM25
// TF saturation component tf*(k1+1) / (tf + k1*(1 - b + b*docLen/avgDocLen)).
func (v *BM25) Vectorize(text string) *vectorstore.SparseVector {
	terms := tokenizeText(text)
	if len(terms) == 0 {
		return &vectorstore.SparseVector{}
	}

	// Term frequencies, accumulated by hashed index (hash collisions merge)
	tf := make(map[uint32]float32, len(terms))
	for _, term := range terms {
		tf[hashTerm(term)]++
	}

	docLen := float32(len(terms))
	norm := v.k1 * (1 - v.b + v.b*docLen/v.avgDocLen)

	return toSortedVector(tf, func(f float32) float32 {
		return f * (v.k1 + 1) / (f + norm)
	})
}

// VectorizeQuery produces a query-side sparse vector: weight 1.0 per unique
// term (the standard BM25 query form; IDF is applied by the store).
func (v *BM25) VectorizeQuery(text string) *vectorstore.SparseVector {
	terms := tokenizeText(text)
	if len(terms) == 0 {
		return &vectorstore.SparseVector{}
	}

	seen := make(map[uint32]float32, len(terms))
	for _, term := range terms {
		seen[hashTerm(term)] = 1.0
	}

	return toSortedVector(seen, func(f float32) float32 { return f })
}

// tokenizeText lowercases and splits on non-alphanumeric runes, dropping
// single-character tokens and stopwords. No stemming: deterministic and
// language-agnostic beyond the stopword list.
func tokenizeText(text string) []string {
	fields := strings.FieldsFunc(strings.ToLower(text), func(r rune) bool {
		return !unicode.IsLetter(r) && !unicode.IsDigit(r)
	})
	terms := fields[:0]
	for _, f := range fields {
		if len(f) < 2 {
			continue
		}
		if _, stop := stopwords[f]; stop {
			continue
		}
		terms = append(terms, f)
	}
	return terms
}

// hashTerm maps a term to a sparse-vector index via FNV-32a.
func hashTerm(term string) uint32 {
	h := fnv.New32a()
	h.Write([]byte(term))
	return h.Sum32()
}

// toSortedVector converts a map of index->value into a SparseVector with
// deterministic (ascending index) ordering, applying transform to each value.
func toSortedVector(m map[uint32]float32, transform func(float32) float32) *vectorstore.SparseVector {
	indices := make([]uint32, 0, len(m))
	for idx := range m {
		indices = append(indices, idx)
	}
	sort.Slice(indices, func(i, j int) bool { return indices[i] < indices[j] })

	values := make([]float32, len(indices))
	for i, idx := range indices {
		values[i] = transform(m[idx])
	}

	return &vectorstore.SparseVector{Indices: indices, Values: values}
}
