package llm

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// OpenAIClient implements LLM against an OpenAI-compatible chat-completions
// API. It works with llama.cpp's llama-server (/v1/chat/completions), vLLM,
// Ollama's compatibility endpoint, and similar servers.
type OpenAIClient struct {
	baseURL string // e.g. "http://localhost:8081/v1"
	model   string
	apiKey  string // optional bearer token
	client  *http.Client
}

// OpenAIOption configures an OpenAIClient.
type OpenAIOption func(*OpenAIClient)

// WithOpenAIBaseURL sets the API base URL (must include the /v1 prefix).
func WithOpenAIBaseURL(url string) OpenAIOption {
	return func(c *OpenAIClient) { c.baseURL = strings.TrimRight(url, "/") }
}

// WithOpenAIModel sets the default model name.
func WithOpenAIModel(model string) OpenAIOption {
	return func(c *OpenAIClient) { c.model = model }
}

// WithOpenAIAPIKey sets an optional bearer token (llama.cpp with --api-key).
func WithOpenAIAPIKey(key string) OpenAIOption {
	return func(c *OpenAIClient) { c.apiKey = key }
}

// NewOpenAIClient creates a client for an OpenAI-compatible server.
func NewOpenAIClient(opts ...OpenAIOption) *OpenAIClient {
	c := &OpenAIClient{
		baseURL: "http://localhost:8081/v1",
		client:  &http.Client{Timeout: 5 * time.Minute},
	}
	for _, opt := range opts {
		opt(c)
	}
	return c
}

type chatMessage struct {
	Role    string `json:"role"`
	Content string `json:"content"`
}

type chatRequest struct {
	Model       string        `json:"model,omitempty"`
	Messages    []chatMessage `json:"messages"`
	Temperature float32       `json:"temperature"`
	MaxTokens   int           `json:"max_tokens,omitempty"`
	Stream      bool          `json:"stream"`
}

type chatResponse struct {
	Choices []struct {
		Message struct {
			Content string `json:"content"`
		} `json:"message"`
		Delta struct {
			Content string `json:"content"`
		} `json:"delta"`
	} `json:"choices"`
}

func (c *OpenAIClient) buildRequest(prompt string, opts GenerateOptions, stream bool) chatRequest {
	var messages []chatMessage
	if opts.SystemPrompt != "" {
		messages = append(messages, chatMessage{Role: "system", Content: opts.SystemPrompt})
	}
	messages = append(messages, chatMessage{Role: "user", Content: prompt})

	model := opts.Model
	if model == "" {
		model = c.model
	}

	return chatRequest{
		Model:       model,
		Messages:    messages,
		Temperature: opts.Temperature,
		MaxTokens:   opts.MaxTokens,
		Stream:      stream,
	}
}

func (c *OpenAIClient) post(ctx context.Context, body chatRequest, client *http.Client) (*http.Response, error) {
	jsonBody, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/chat/completions", bytes.NewReader(jsonBody))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	if c.apiKey != "" {
		req.Header.Set("Authorization", "Bearer "+c.apiKey)
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("LLM API error (status %d): %s", resp.StatusCode, string(body))
	}
	return resp, nil
}

// Generate produces a completion for the prompt.
func (c *OpenAIClient) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	resp, err := c.post(ctx, c.buildRequest(prompt, opts, false), c.client)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	var out chatResponse
	if err := json.NewDecoder(resp.Body).Decode(&out); err != nil {
		return "", fmt.Errorf("failed to decode response: %w", err)
	}
	if len(out.Choices) == 0 {
		return "", fmt.Errorf("empty response from LLM")
	}
	return out.Choices[0].Message.Content, nil
}

// GenerateStream produces a token stream via SSE.
func (c *OpenAIClient) GenerateStream(ctx context.Context, prompt string, opts GenerateOptions) (<-chan StreamChunk, error) {
	// No overall timeout on streaming; the context bounds it
	resp, err := c.post(ctx, c.buildRequest(prompt, opts, true), &http.Client{})
	if err != nil {
		return nil, err
	}

	ch := make(chan StreamChunk)
	go func() {
		defer close(ch)
		defer resp.Body.Close()

		scanner := bufio.NewScanner(resp.Body)
		scanner.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			if !strings.HasPrefix(line, "data:") {
				continue
			}
			data := strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if data == "[DONE]" {
				break
			}

			var chunk chatResponse
			if err := json.Unmarshal([]byte(data), &chunk); err != nil {
				continue // skip malformed keep-alives
			}
			if len(chunk.Choices) == 0 {
				continue
			}
			token := chunk.Choices[0].Delta.Content
			if token == "" {
				continue
			}

			select {
			case ch <- StreamChunk{Token: token}:
			case <-ctx.Done():
				ch <- StreamChunk{Error: ctx.Err()}
				return
			}
		}
		if err := scanner.Err(); err != nil {
			ch <- StreamChunk{Error: fmt.Errorf("stream read error: %w", err)}
			return
		}
		ch <- StreamChunk{Done: true}
	}()

	return ch, nil
}

// Ensure OpenAIClient implements LLM.
var _ LLM = (*OpenAIClient)(nil)
