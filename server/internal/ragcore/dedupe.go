package ragcore

import (
	"strings"

	"github.com/knoguchi/rag/internal/ragcore/vectorstore"
)

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
