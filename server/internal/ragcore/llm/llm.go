// Package llm provides interfaces and implementations for Large Language Model clients.
package llm

import (
	"context"
)

// GenerateOptions configures the LLM generation request.
type GenerateOptions struct {
	// Model specifies the LLM model to use (e.g., "llama3.2", "mistral").
	Model string

	// SystemPrompt sets the system-level instructions for the model.
	SystemPrompt string

	// Temperature controls randomness in generation (0.0 = deterministic, 1.0 = creative).
	Temperature float32

	// MaxTokens limits the maximum number of tokens in the response.
	MaxTokens int
}

// StreamChunk represents a single chunk of streamed response from the LLM.
type StreamChunk struct {
	// Token contains the generated text fragment.
	Token string

	// Done indicates whether this is the final chunk in the stream.
	Done bool

	// Error contains any error that occurred during streaming.
	Error error
}

// LLM defines the interface for Large Language Model clients.
type LLM interface {
	// Generate sends a prompt to the LLM and returns the complete response.
	// It blocks until the full response is received or an error occurs.
	Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error)

	// GenerateStream sends a prompt to the LLM and returns a channel that streams
	// response chunks as they are generated. The channel is closed when generation
	// completes or an error occurs. Callers should check StreamChunk.Error and
	// StreamChunk.Done to detect completion and errors.
	GenerateStream(ctx context.Context, prompt string, opts GenerateOptions) (<-chan StreamChunk, error)
}
