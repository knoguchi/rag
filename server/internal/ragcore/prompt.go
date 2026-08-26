package ragcore

import (
	"fmt"
	"strings"

	"github.com/knoguchi/rag/internal/ragcore/memory"
)

// buildRAGPrompt constructs the RAG prompt with metadata, conversation
// history, and the retrieved context documents.
func buildRAGPrompt(systemPrompt string, chunks []RetrievedChunk, query string, history []memory.Message) string {
	var sb strings.Builder

	// System instructions
	sb.WriteString(systemPrompt)
	sb.WriteString("\n\n")

	// Conversation history (if any)
	if len(history) > 0 {
		sb.WriteString("## Conversation History\n")
		sb.WriteString("(Previous exchanges in this session for context)\n\n")
		sb.WriteString(memory.FormatForPrompt(history))
		sb.WriteString("\n")
	}

	// Context section with metadata (relevance scores omitted to avoid biasing LLM)
	sb.WriteString("## Context Documents\n\n")
	for i, chunk := range chunks {
		sb.WriteString(fmt.Sprintf("[Doc %d]", i+1))

		if chunk.Title != "" {
			sb.WriteString(fmt.Sprintf(" (Title: %s)", chunk.Title))
		}
		if chunk.Source != "" {
			sb.WriteString(fmt.Sprintf(" (Source: %s)", chunk.Source))
		}
		sb.WriteString("\n")
		sb.WriteString(chunk.Content)
		sb.WriteString("\n\n")
	}

	// Question
	sb.WriteString("## Question\n")
	sb.WriteString(query)
	sb.WriteString("\n\n")

	// Direct answer prompt (no chain-of-thought to keep responses concise)
	sb.WriteString("## Answer (be brief and direct)\n")

	return sb.String()
}
