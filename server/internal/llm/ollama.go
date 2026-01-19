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

const (
	// DefaultOllamaBaseURL is the default Ollama API endpoint.
	DefaultOllamaBaseURL = "http://localhost:11434"

	// DefaultModel is the default LLM model to use.
	DefaultModel = "llama3.2"

	// DefaultTemperature is the default generation temperature.
	// Lower temperature (0.3) for more deterministic, factual responses in RAG.
	DefaultTemperature = 0.3

	// DefaultMaxTokens is the default maximum tokens (0 means no limit).
	DefaultMaxTokens = 0
)

// OllamaClient implements the LLM interface using the Ollama API.
type OllamaClient struct {
	baseURL    string
	httpClient *http.Client
	model      string
}

// OllamaOption is a functional option for configuring OllamaClient.
type OllamaOption func(*OllamaClient)

// WithBaseURL sets a custom base URL for the Ollama API.
func WithBaseURL(url string) OllamaOption {
	return func(c *OllamaClient) {
		c.baseURL = strings.TrimSuffix(url, "/")
	}
}

// WithHTTPClient sets a custom HTTP client.
func WithHTTPClient(client *http.Client) OllamaOption {
	return func(c *OllamaClient) {
		c.httpClient = client
	}
}

// WithModel sets the default model for the client.
func WithModel(model string) OllamaOption {
	return func(c *OllamaClient) {
		c.model = model
	}
}

// NewOllamaClient creates a new Ollama LLM client with the given options.
func NewOllamaClient(opts ...OllamaOption) *OllamaClient {
	c := &OllamaClient{
		baseURL: DefaultOllamaBaseURL,
		httpClient: &http.Client{
			Timeout: 5 * time.Minute, // Long timeout for generation
		},
		model: DefaultModel,
	}

	for _, opt := range opts {
		opt(c)
	}

	return c
}

// ollamaRequest represents the request body for Ollama's generate API.
type ollamaRequest struct {
	Model       string                 `json:"model"`
	Prompt      string                 `json:"prompt"`
	System      string                 `json:"system,omitempty"`
	Stream      bool                   `json:"stream"`
	Options     map[string]interface{} `json:"options,omitempty"`
}

// ollamaResponse represents the response from Ollama's generate API.
type ollamaResponse struct {
	Model              string    `json:"model"`
	CreatedAt          time.Time `json:"created_at"`
	Response           string    `json:"response"`
	Done               bool      `json:"done"`
	DoneReason         string    `json:"done_reason,omitempty"`
	Context            []int     `json:"context,omitempty"`
	TotalDuration      int64     `json:"total_duration,omitempty"`
	LoadDuration       int64     `json:"load_duration,omitempty"`
	PromptEvalCount    int       `json:"prompt_eval_count,omitempty"`
	PromptEvalDuration int64     `json:"prompt_eval_duration,omitempty"`
	EvalCount          int       `json:"eval_count,omitempty"`
	EvalDuration       int64     `json:"eval_duration,omitempty"`
}

// Generate sends a prompt to Ollama and returns the complete response.
func (c *OllamaClient) Generate(ctx context.Context, prompt string, opts GenerateOptions) (string, error) {
	req, err := c.buildRequest(ctx, prompt, opts, false)
	if err != nil {
		return "", fmt.Errorf("building request: %w", err)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("executing request: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		return "", fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	var result ollamaResponse
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return "", fmt.Errorf("decoding response: %w", err)
	}

	return result.Response, nil
}

// GenerateStream sends a prompt to Ollama and returns a channel that streams response chunks.
func (c *OllamaClient) GenerateStream(ctx context.Context, prompt string, opts GenerateOptions) (<-chan StreamChunk, error) {
	req, err := c.buildRequest(ctx, prompt, opts, true)
	if err != nil {
		return nil, fmt.Errorf("building request: %w", err)
	}

	// Create a client without timeout for streaming (context handles cancellation)
	streamClient := &http.Client{}
	resp, err := streamClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("executing request: %w", err)
	}

	if resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()
		return nil, fmt.Errorf("ollama API error (status %d): %s", resp.StatusCode, string(body))
	}

	chunks := make(chan StreamChunk)

	go func() {
		defer close(chunks)
		defer resp.Body.Close()

		reader := bufio.NewReader(resp.Body)

		for {
			select {
			case <-ctx.Done():
				chunks <- StreamChunk{Error: ctx.Err(), Done: true}
				return
			default:
			}

			line, err := reader.ReadBytes('\n')
			if err != nil {
				if err == io.EOF {
					return
				}
				chunks <- StreamChunk{Error: fmt.Errorf("reading stream: %w", err), Done: true}
				return
			}

			// Skip empty lines
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}

			var streamResp ollamaResponse
			if err := json.Unmarshal(line, &streamResp); err != nil {
				chunks <- StreamChunk{Error: fmt.Errorf("parsing stream response: %w", err), Done: true}
				return
			}

			chunk := StreamChunk{
				Token: streamResp.Response,
				Done:  streamResp.Done,
			}

			select {
			case <-ctx.Done():
				chunks <- StreamChunk{Error: ctx.Err(), Done: true}
				return
			case chunks <- chunk:
			}

			if streamResp.Done {
				return
			}
		}
	}()

	return chunks, nil
}

// buildRequest constructs the HTTP request for the Ollama API.
func (c *OllamaClient) buildRequest(ctx context.Context, prompt string, opts GenerateOptions, stream bool) (*http.Request, error) {
	model := opts.Model
	if model == "" {
		model = c.model
	}

	reqBody := ollamaRequest{
		Model:  model,
		Prompt: prompt,
		System: opts.SystemPrompt,
		Stream: stream,
	}

	// Build options map for temperature and max tokens
	options := make(map[string]interface{})
	if opts.Temperature > 0 {
		options["temperature"] = opts.Temperature
	}
	if opts.MaxTokens > 0 {
		options["num_predict"] = opts.MaxTokens
	}
	if len(options) > 0 {
		reqBody.Options = options
	}

	body, err := json.Marshal(reqBody)
	if err != nil {
		return nil, fmt.Errorf("marshaling request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/api/generate", bytes.NewReader(body))
	if err != nil {
		return nil, fmt.Errorf("creating request: %w", err)
	}

	req.Header.Set("Content-Type", "application/json")

	return req, nil
}

// Ensure OllamaClient implements LLM interface.
var _ LLM = (*OllamaClient)(nil)
