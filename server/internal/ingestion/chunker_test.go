package ingestion

import (
	"strings"
	"testing"

	"github.com/knoguchi/rag/internal/repository"
)

func TestNewChunker_Defaults(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{})

	// Should apply defaults
	if chunker.config.TargetSize != 512 {
		t.Errorf("expected default TargetSize 512, got %d", chunker.config.TargetSize)
	}
	if chunker.config.MaxSize != 1024 {
		t.Errorf("expected default MaxSize 1024, got %d", chunker.config.MaxSize)
	}
	if chunker.config.Method != "semantic" {
		t.Errorf("expected default Method 'semantic', got %s", chunker.config.Method)
	}
}

func TestChunker_EmptyContent(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{Method: "fixed"})

	chunks := chunker.Chunk("")
	if chunks != nil {
		t.Errorf("expected nil for empty content, got %v", chunks)
	}

	chunks = chunker.Chunk("   ")
	if chunks != nil {
		t.Errorf("expected nil for whitespace content, got %v", chunks)
	}
}

func TestChunker_FixedMethod(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{
		Method:     "fixed",
		TargetSize: 10, // 10 words per chunk
		MaxSize:    20,
		Overlap:    2,
	})

	// Create content with 25 words
	words := make([]string, 25)
	for i := range words {
		words[i] = "word"
	}
	content := strings.Join(words, " ")

	chunks := chunker.Chunk(content)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	// Each chunk should have metadata
	for i, chunk := range chunks {
		if chunk.Index != i {
			t.Errorf("chunk %d has wrong index %d", i, chunk.Index)
		}
		if chunk.Metadata["method"] != "fixed" {
			t.Errorf("chunk %d has wrong method %s", i, chunk.Metadata["method"])
		}
		if chunk.Content == "" {
			t.Errorf("chunk %d has empty content", i)
		}
	}
}

func TestChunker_SentenceMethod(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{
		Method:     "sentence",
		TargetSize: 20,
		MaxSize:    50,
		Overlap:    5,
	})

	content := "This is the first sentence. This is the second sentence. This is the third sentence."

	chunks := chunker.Chunk(content)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for _, chunk := range chunks {
		if chunk.Metadata["method"] != "sentence" {
			t.Errorf("expected method 'sentence', got %s", chunk.Metadata["method"])
		}
	}
}

func TestChunker_SemanticMethod(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{
		Method:     "semantic",
		TargetSize: 50,
		MaxSize:    100,
		Overlap:    10,
	})

	content := `# Introduction

This is the introduction paragraph with some content.

## Getting Started

Here is how you get started with the project.

### Installation

Run the following command to install.
`

	chunks := chunker.Chunk(content)

	if len(chunks) == 0 {
		t.Fatal("expected at least one chunk")
	}

	for _, chunk := range chunks {
		if chunk.Metadata["method"] != "semantic" {
			t.Errorf("expected method 'semantic', got %s", chunk.Metadata["method"])
		}
	}
}

func TestChunker_SemanticPreservesCodeBlocks(t *testing.T) {
	chunker := NewChunker(repository.ChunkerConfig{
		Method:     "semantic",
		TargetSize: 20,
		MaxSize:    100,
		Overlap:    0,
	})

	content := `# Code Example

Here is some code:

` + "```go\nfunc main() {\n    fmt.Println(\"Hello\")\n}\n```" + `

And some more text after the code.
`

	chunks := chunker.Chunk(content)

	// Find chunk with code
	foundCode := false
	for _, chunk := range chunks {
		if strings.Contains(chunk.Content, "func main()") {
			foundCode = true
			if chunk.Metadata["contains_code"] != "true" {
				t.Error("expected contains_code metadata for code chunk")
			}
		}
	}

	if !foundCode {
		t.Error("code block was not preserved in any chunk")
	}
}

func TestSplitSentences(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected int // expected number of sentences
	}{
		{
			name:     "empty",
			input:    "",
			expected: 0,
		},
		{
			name:     "single sentence",
			input:    "This is a sentence.",
			expected: 1,
		},
		{
			name:     "multiple sentences",
			input:    "First sentence. Second sentence. Third sentence.",
			expected: 3,
		},
		{
			name:     "with exclamation",
			input:    "Hello! How are you? I am fine.",
			expected: 3,
		},
		{
			name:     "no ending punctuation",
			input:    "This has no ending punctuation",
			expected: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			sentences := splitSentences(tt.input)
			if len(sentences) != tt.expected {
				t.Errorf("expected %d sentences, got %d: %v", tt.expected, len(sentences), sentences)
			}
		})
	}
}

func TestIsAbbreviation(t *testing.T) {
	tests := []struct {
		input    string
		expected bool
	}{
		{"Dr.", true},
		{"Mr.", true},
		{"e.g.", true},
		{"etc.", true},
		{"Hello.", false},
		{"sentence.", false},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := isAbbreviation(tt.input)
			if result != tt.expected {
				t.Errorf("isAbbreviation(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}

func TestEstimateTokens(t *testing.T) {
	tests := []struct {
		input    string
		expected int
	}{
		{"", 0},
		{"hello", 1},
		{"hello world", 2},
		{"one two three four five", 5},
	}

	for _, tt := range tests {
		t.Run(tt.input, func(t *testing.T) {
			result := estimateTokens(tt.input)
			if result != tt.expected {
				t.Errorf("estimateTokens(%q) = %d, expected %d", tt.input, result, tt.expected)
			}
		})
	}
}

func TestIsListBlock(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected bool
	}{
		{"dash list", "- item 1\n- item 2", true},
		{"asterisk list", "* item 1\n* item 2", true},
		{"plus list", "+ item 1\n+ item 2", true},
		{"numbered list", "1. First\n2. Second", true},
		{"paragraph", "This is a regular paragraph.", false},
		{"empty", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isListBlock(tt.input)
			if result != tt.expected {
				t.Errorf("isListBlock(%q) = %v, expected %v", tt.input, result, tt.expected)
			}
		})
	}
}
