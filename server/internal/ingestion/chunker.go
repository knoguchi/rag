// Package ingestion handles document processing: chunking, text extraction, and pipeline orchestration.
package ingestion

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/knoguchi/rag/internal/repository"
)

// Chunk represents a piece of chunked content
type Chunk struct {
	Content  string
	Index    int
	Metadata map[string]string
}

// Chunker handles text chunking with different strategies
type Chunker struct {
	config repository.ChunkerConfig
}

// NewChunker creates a new Chunker with the given configuration
func NewChunker(config repository.ChunkerConfig) *Chunker {
	// Apply defaults if not set
	if config.TargetSize <= 0 {
		config.TargetSize = 512
	}
	if config.MaxSize <= 0 {
		config.MaxSize = 1024
	}
	if config.Overlap < 0 {
		config.Overlap = 50
	}
	if config.Method == "" {
		config.Method = "semantic"
	}

	return &Chunker{config: config}
}

// Chunk splits content into chunks based on the configured method
func (c *Chunker) Chunk(content string) []Chunk {
	content = strings.TrimSpace(content)
	if content == "" {
		return nil
	}

	switch c.config.Method {
	case "fixed":
		return c.chunkFixed(content)
	case "sentence":
		return c.chunkSentence(content)
	case "semantic":
		return c.chunkSemantic(content)
	default:
		// Default to semantic if unknown method
		return c.chunkSemantic(content)
	}
}

// estimateTokens approximates token count from text
// Uses the heuristic: tokens ≈ words / 0.75
func estimateTokens(text string) int {
	words := len(strings.Fields(text))
	// tokens ≈ words / 0.75, which is words * 1.33
	// For simplicity, we use word count as a reasonable proxy
	return words
}

// estimateTokensFromWords converts word count to approximate token count
func estimateTokensFromWords(wordCount int) int {
	return wordCount
}

// ============================================================================
// Fixed Chunking
// ============================================================================

// chunkFixed splits content into fixed-size chunks with overlap
func (c *Chunker) chunkFixed(content string) []Chunk {
	words := strings.Fields(content)
	if len(words) == 0 {
		return nil
	}

	var chunks []Chunk
	targetWords := c.config.TargetSize // Using word count as token proxy
	overlapWords := c.config.Overlap

	for i := 0; i < len(words); {
		end := i + targetWords
		if end > len(words) {
			end = len(words)
		}

		chunkWords := words[i:end]
		chunkContent := strings.Join(chunkWords, " ")

		chunks = append(chunks, Chunk{
			Content: chunkContent,
			Index:   len(chunks),
			Metadata: map[string]string{
				"method":     "fixed",
				"word_count": intToString(len(chunkWords)),
			},
		})

		// Move forward by target minus overlap
		step := targetWords - overlapWords
		if step <= 0 {
			step = targetWords / 2
			if step <= 0 {
				step = 1
			}
		}
		i += step

		// If we've already captured everything, break
		if end >= len(words) {
			break
		}
	}

	return chunks
}

// ============================================================================
// Sentence Chunking
// ============================================================================

// chunkSentence groups sentences until target size is reached
func (c *Chunker) chunkSentence(content string) []Chunk {
	sentences := splitSentences(content)
	if len(sentences) == 0 {
		return nil
	}

	var chunks []Chunk
	var currentSentences []string
	currentWordCount := 0

	for _, sentence := range sentences {
		sentenceWords := len(strings.Fields(sentence))

		// If adding this sentence would exceed max size and we have content, flush
		if currentWordCount+sentenceWords > c.config.MaxSize && currentWordCount > 0 {
			chunks = append(chunks, c.createSentenceChunk(currentSentences, len(chunks)))

			// Calculate overlap - keep last few sentences if possible
			currentSentences, currentWordCount = c.calculateSentenceOverlap(currentSentences)
		}

		// If single sentence exceeds max, split it by words
		if sentenceWords > c.config.MaxSize {
			// Flush current content first
			if currentWordCount > 0 {
				chunks = append(chunks, c.createSentenceChunk(currentSentences, len(chunks)))
				currentSentences = nil
				currentWordCount = 0
			}
			// Split the long sentence
			splitChunks := c.splitLongSentence(sentence, len(chunks))
			chunks = append(chunks, splitChunks...)
			continue
		}

		currentSentences = append(currentSentences, sentence)
		currentWordCount += sentenceWords

		// If we've reached target size, flush
		if currentWordCount >= c.config.TargetSize {
			chunks = append(chunks, c.createSentenceChunk(currentSentences, len(chunks)))
			currentSentences, currentWordCount = c.calculateSentenceOverlap(currentSentences)
		}
	}

	// Flush remaining content
	if len(currentSentences) > 0 {
		chunks = append(chunks, c.createSentenceChunk(currentSentences, len(chunks)))
	}

	return chunks
}

// createSentenceChunk creates a chunk from sentences
func (c *Chunker) createSentenceChunk(sentences []string, index int) Chunk {
	content := strings.Join(sentences, " ")
	return Chunk{
		Content: strings.TrimSpace(content),
		Index:   index,
		Metadata: map[string]string{
			"method":         "sentence",
			"sentence_count": intToString(len(sentences)),
			"word_count":     intToString(len(strings.Fields(content))),
		},
	}
}

// calculateSentenceOverlap calculates which sentences to keep for overlap
func (c *Chunker) calculateSentenceOverlap(sentences []string) ([]string, int) {
	if c.config.Overlap <= 0 || len(sentences) == 0 {
		return nil, 0
	}

	var overlapSentences []string
	overlapWords := 0

	// Work backwards from the end to collect overlap
	for i := len(sentences) - 1; i >= 0 && overlapWords < c.config.Overlap; i-- {
		sentenceWords := len(strings.Fields(sentences[i]))
		overlapSentences = append([]string{sentences[i]}, overlapSentences...)
		overlapWords += sentenceWords
	}

	return overlapSentences, overlapWords
}

// splitLongSentence splits a sentence that exceeds max size
func (c *Chunker) splitLongSentence(sentence string, startIndex int) []Chunk {
	words := strings.Fields(sentence)
	var chunks []Chunk

	for i := 0; i < len(words); {
		end := i + c.config.TargetSize
		if end > len(words) {
			end = len(words)
		}

		chunkWords := words[i:end]
		content := strings.Join(chunkWords, " ")

		chunks = append(chunks, Chunk{
			Content: content,
			Index:   startIndex + len(chunks),
			Metadata: map[string]string{
				"method":     "sentence",
				"word_count": intToString(len(chunkWords)),
				"split":      "true",
			},
		})

		step := c.config.TargetSize - c.config.Overlap
		if step <= 0 {
			step = c.config.TargetSize / 2
			if step <= 0 {
				step = 1
			}
		}
		i += step

		if end >= len(words) {
			break
		}
	}

	return chunks
}

// ============================================================================
// Semantic Chunking (Markdown-Aware)
// ============================================================================

// contentBlock represents a semantic block of content
type contentBlock struct {
	blockType string // "header", "paragraph", "code", "table", "list"
	content   string
	header    string // Current section header context
	level     int    // Header level (1-6)
}

// chunkSemantic performs smart semantic chunking that:
// 1. Preserves code blocks and tables as atomic units
// 2. Keeps header context for each chunk
// 3. Groups related paragraphs together
func (c *Chunker) chunkSemantic(content string) []Chunk {
	// Step 1: Parse content into semantic blocks
	blocks := c.parseIntoBlocks(content)

	// Step 2: Group blocks into chunks respecting size limits
	chunks := c.groupBlocksIntoChunks(blocks)

	// Step 3: Add overlap between chunks
	if c.config.Overlap > 0 {
		chunks = c.addSemanticOverlap(chunks)
	}

	// Renumber chunks sequentially
	for i := range chunks {
		chunks[i].Index = i
	}

	return chunks
}

// parseIntoBlocks parses markdown content into semantic blocks
func (c *Chunker) parseIntoBlocks(content string) []contentBlock {
	var blocks []contentBlock
	currentHeader := ""
	currentLevel := 0

	// Patterns for detecting different block types
	headerPattern := regexp.MustCompile(`(?m)^(#{1,6})\s+(.+)$`)
	codeBlockPattern := regexp.MustCompile("(?s)```(\\w*)\\n(.*?)```")
	tablePattern := regexp.MustCompile(`(?m)^\|.+\|$`)

	// Find all code blocks first and replace with placeholders
	codeBlocks := codeBlockPattern.FindAllStringSubmatchIndex(content, -1)
	codeBlockMap := make(map[string]string)

	// Process content, replacing code blocks with placeholders
	processedContent := content
	for i := len(codeBlocks) - 1; i >= 0; i-- {
		match := codeBlocks[i]
		codeContent := content[match[0]:match[1]]
		placeholder := "___CODE_BLOCK_" + strconv.Itoa(i) + "___"
		codeBlockMap[placeholder] = codeContent
		processedContent = processedContent[:match[0]] + placeholder + processedContent[match[1]:]
	}

	// Split by double newlines to get paragraphs
	paragraphs := regexp.MustCompile(`\n\s*\n`).Split(processedContent, -1)

	for _, para := range paragraphs {
		para = strings.TrimSpace(para)
		if para == "" {
			continue
		}

		// Check if this is a code block placeholder
		if strings.HasPrefix(para, "___CODE_BLOCK_") && strings.HasSuffix(para, "___") {
			if codeContent, ok := codeBlockMap[para]; ok {
				blocks = append(blocks, contentBlock{
					blockType: "code",
					content:   codeContent,
					header:    currentHeader,
					level:     currentLevel,
				})
				continue
			}
		}

		// Check if this is a header
		if headerMatch := headerPattern.FindStringSubmatch(para); headerMatch != nil {
			currentLevel = len(headerMatch[1])
			currentHeader = headerMatch[2]
			blocks = append(blocks, contentBlock{
				blockType: "header",
				content:   para,
				header:    currentHeader,
				level:     currentLevel,
			})
			continue
		}

		// Check if this is a table
		if tablePattern.MatchString(para) {
			blocks = append(blocks, contentBlock{
				blockType: "table",
				content:   para,
				header:    currentHeader,
				level:     currentLevel,
			})
			continue
		}

		// Check if this is a list
		if isListBlock(para) {
			blocks = append(blocks, contentBlock{
				blockType: "list",
				content:   para,
				header:    currentHeader,
				level:     currentLevel,
			})
			continue
		}

		// Regular paragraph
		blocks = append(blocks, contentBlock{
			blockType: "paragraph",
			content:   para,
			header:    currentHeader,
			level:     currentLevel,
		})
	}

	return blocks
}

// isListBlock checks if a block is a list
func isListBlock(content string) bool {
	lines := strings.Split(content, "\n")
	if len(lines) == 0 {
		return false
	}
	firstLine := strings.TrimSpace(lines[0])
	return strings.HasPrefix(firstLine, "- ") ||
		strings.HasPrefix(firstLine, "* ") ||
		strings.HasPrefix(firstLine, "+ ") ||
		regexp.MustCompile(`^\d+\.\s`).MatchString(firstLine)
}

// groupBlocksIntoChunks groups blocks into appropriately sized chunks
func (c *Chunker) groupBlocksIntoChunks(blocks []contentBlock) []Chunk {
	var chunks []Chunk
	var currentBlocks []contentBlock
	currentWords := 0
	currentHeader := ""

	flushChunk := func() {
		if len(currentBlocks) == 0 {
			return
		}

		// Build chunk content with context
		var contentParts []string

		// Add header context if available
		headerAdded := false
		for _, block := range currentBlocks {
			if block.header != "" && !headerAdded {
				// Add contextual header as a prefix for better retrieval
				prefix := strings.Repeat("#", block.level) + " " + block.header
				if currentBlocks[0].blockType != "header" || currentBlocks[0].content != prefix {
					contentParts = append(contentParts, "[Section: "+block.header+"]")
					headerAdded = true
				}
			}
			contentParts = append(contentParts, block.content)
		}

		content := strings.Join(contentParts, "\n\n")
		wordCount := len(strings.Fields(content))

		metadata := map[string]string{
			"method":     "semantic",
			"word_count": intToString(wordCount),
		}

		// Determine primary block type
		blockTypes := make(map[string]int)
		for _, block := range currentBlocks {
			blockTypes[block.blockType]++
		}
		if blockTypes["code"] > 0 {
			metadata["contains_code"] = "true"
		}
		if blockTypes["table"] > 0 {
			metadata["contains_table"] = "true"
		}
		if currentHeader != "" {
			metadata["section"] = currentHeader
		}

		chunks = append(chunks, Chunk{
			Content:  strings.TrimSpace(content),
			Index:    len(chunks),
			Metadata: metadata,
		})

		currentBlocks = nil
		currentWords = 0
	}

	for _, block := range blocks {
		blockWords := len(strings.Fields(block.content))

		// Update current header context
		if block.blockType == "header" {
			currentHeader = block.header
		}

		// Code blocks and tables are kept as atomic units if possible
		isAtomic := block.blockType == "code" || block.blockType == "table"

		// If this block alone exceeds max size, it goes in its own chunk
		if blockWords > c.config.MaxSize {
			// Flush current chunk first
			flushChunk()

			// For large atomic blocks, keep them whole even if they exceed limits
			if isAtomic {
				currentBlocks = append(currentBlocks, block)
				flushChunk()
			} else {
				// Split large paragraphs
				splitChunks := c.splitLargeBlock(block)
				chunks = append(chunks, splitChunks...)
			}
			continue
		}

		// Check if adding this block would exceed limits
		if currentWords+blockWords > c.config.TargetSize && currentWords > 0 {
			// For atomic blocks, try to keep them with context if possible
			if isAtomic && currentWords+blockWords <= c.config.MaxSize {
				// Keep the atomic block with current context
				currentBlocks = append(currentBlocks, block)
				currentWords += blockWords
				flushChunk()
				continue
			}

			// Flush current chunk
			flushChunk()
		}

		currentBlocks = append(currentBlocks, block)
		currentWords += blockWords
	}

	// Flush remaining content
	flushChunk()

	return chunks
}

// splitLargeBlock splits a large block that exceeds max size
func (c *Chunker) splitLargeBlock(block contentBlock) []Chunk {
	var chunks []Chunk
	sentences := splitSentences(block.content)

	var currentSentences []string
	currentWords := 0

	for _, sentence := range sentences {
		sentenceWords := len(strings.Fields(sentence))

		if currentWords+sentenceWords > c.config.TargetSize && currentWords > 0 {
			content := strings.Join(currentSentences, " ")

			// Add section context
			if block.header != "" {
				content = "[Section: " + block.header + "]\n\n" + content
			}

			chunks = append(chunks, Chunk{
				Content: strings.TrimSpace(content),
				Index:   len(chunks),
				Metadata: map[string]string{
					"method":     "semantic",
					"word_count": intToString(currentWords),
					"section":    block.header,
					"split":      "true",
				},
			})

			currentSentences = nil
			currentWords = 0
		}

		currentSentences = append(currentSentences, sentence)
		currentWords += sentenceWords
	}

	// Flush remaining
	if len(currentSentences) > 0 {
		content := strings.Join(currentSentences, " ")
		if block.header != "" {
			content = "[Section: " + block.header + "]\n\n" + content
		}

		chunks = append(chunks, Chunk{
			Content: strings.TrimSpace(content),
			Index:   len(chunks),
			Metadata: map[string]string{
				"method":     "semantic",
				"word_count": intToString(currentWords),
				"section":    block.header,
				"split":      "true",
			},
		})
	}

	return chunks
}

// addSemanticOverlap adds contextual overlap between chunks
func (c *Chunker) addSemanticOverlap(chunks []Chunk) []Chunk {
	if len(chunks) <= 1 {
		return chunks
	}

	result := make([]Chunk, len(chunks))

	for i, chunk := range chunks {
		result[i] = Chunk{
			Content:  chunk.Content,
			Index:    chunk.Index,
			Metadata: copyMetadata(chunk.Metadata),
		}

		// Add context from previous chunk
		if i > 0 && c.config.Overlap > 0 {
			prevContent := chunks[i-1].Content
			prevWords := strings.Fields(prevContent)

			if len(prevWords) > 0 {
				overlapCount := c.config.Overlap
				if overlapCount > len(prevWords) {
					overlapCount = len(prevWords)
				}

				// Get the last N words from previous chunk
				overlapWords := prevWords[len(prevWords)-overlapCount:]
				overlapText := strings.Join(overlapWords, " ")

				// Only add overlap if it's meaningful (not just a header)
				if !strings.HasPrefix(overlapText, "[Section:") {
					result[i].Content = "[...] " + overlapText + "\n\n" + result[i].Content
					result[i].Metadata["has_overlap"] = "true"
					result[i].Metadata["overlap_words"] = intToString(overlapCount)
				}
			}
		}
	}

	return result
}

// ============================================================================
// Utility Functions
// ============================================================================

// splitSentences splits text into sentences
func splitSentences(text string) []string {
	// Normalize whitespace
	text = strings.TrimSpace(text)
	if text == "" {
		return nil
	}

	// Simple sentence splitting on . ! ? followed by space or end
	// This is a simplified approach; production would need more sophisticated NLP
	var sentences []string
	var current strings.Builder

	runes := []rune(text)
	for i := 0; i < len(runes); i++ {
		r := runes[i]
		current.WriteRune(r)

		// Check for sentence boundary
		if r == '.' || r == '!' || r == '?' {
			// Look ahead for space or end
			if i+1 >= len(runes) || unicode.IsSpace(runes[i+1]) {
				// Check for common abbreviations (simple heuristic)
				sentence := strings.TrimSpace(current.String())
				if sentence != "" && !isAbbreviation(sentence) {
					sentences = append(sentences, sentence)
					current.Reset()
				}
			}
		}
	}

	// Add remaining text as final sentence
	remaining := strings.TrimSpace(current.String())
	if remaining != "" {
		sentences = append(sentences, remaining)
	}

	return sentences
}

// isAbbreviation checks if a sentence ends with a common abbreviation
func isAbbreviation(text string) bool {
	// Common abbreviations that shouldn't end sentences
	abbreviations := []string{
		"mr.", "mrs.", "ms.", "dr.", "prof.",
		"inc.", "ltd.", "corp.",
		"etc.", "e.g.", "i.e.",
		"vs.", "v.",
		"st.", "ave.", "blvd.",
		"no.", "vol.", "pg.",
	}

	lower := strings.ToLower(text)
	for _, abbr := range abbreviations {
		if strings.HasSuffix(lower, abbr) {
			return true
		}
	}
	return false
}

// intToString converts int to string
func intToString(n int) string {
	return strconv.Itoa(n)
}

// copyMetadata creates a copy of metadata map
func copyMetadata(m map[string]string) map[string]string {
	if m == nil {
		return make(map[string]string)
	}
	result := make(map[string]string, len(m))
	for k, v := range m {
		result[k] = v
	}
	return result
}
